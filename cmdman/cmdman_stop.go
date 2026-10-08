package cmdman

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	cmdmanv1pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
	"github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/cmdman/pkg/hrstr"
	"google.golang.org/protobuf/types/known/durationpb"
)

// defaultStopTimeout is how long a stop waits for the command to go down before
// it escalates to SIGKILL.
const defaultStopTimeout = 10 * time.Second

// StopRequest defines a stop operation across explicit targets and/or labels.
type StopRequest struct {
	Targets []string
	Signal  string
	Timeout time.Duration
}

type StopResult struct {
	ID  string
	Err error
}

func (s *Service) Stop(ctx context.Context, req StopRequest) ([]StopResult, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	ids, err := resolveTargets(st, req.Targets, nil)
	if err != nil {
		return nil, err
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	results := make([]StopResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, StopResult{
			ID:  id,
			Err: s.stop(ctx, st, id, req.Signal, timeout),
		})
	}
	return results, nil
}

// stop stops id through its monitor, escalating to SIGKILL after timeout. A
// monitor that does not answer is taken for dead: stop marks the command failed
// and reports no error.
func (s *Service) stop(
	ctx context.Context,
	st *store.Store,
	id string,
	signalOverride string,
	timeout time.Duration,
) error {
	_, err := s.stopReportingUnreachable(ctx, st, id, signalOverride, timeout)
	return err
}

// stopReportingUnreachable is stop that also reports whether the monitor did
// not answer and died short of recording the end of the run, for a caller that
// has its own way to deal with such a monitor.
func (s *Service) stopReportingUnreachable(
	ctx context.Context,
	st *store.Store,
	id string,
	signalOverride string,
	timeout time.Duration,
) (unreachable bool, err error) {
	state, _, _, err := st.GetCommandState(id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get command state: %w", err)
	}
	if state == model.EventTypeExited || state == model.EventTypeFailed {
		return false, nil
	}

	_, _, cfg, err := st.GetCommandConfig(id)
	if err != nil {
		return false, fmt.Errorf("get command config: %w", err)
	}

	effective := cfg.StopSignal
	if signalOverride != "" {
		effective = signalOverride
	}
	if effective == "" {
		effective = model.DefaultStopSignal
	}
	sig, _, err := hrstr.ParseSignal(effective)
	if err != nil {
		return false, err
	}

	s.emitEvent(ctx, model.Event{
		Time: time.Now().UTC(),
		Type: model.EventTypeStopped,
		ID:   id,
		Attrs: map[string]string{
			"signal": fmt.Sprintf("%d", sig),
		},
	})

	if err := s.sendStop(ctx, st, id, sig, timeout); err != nil {
		if isMonitorUnavailable(err) {
			// From the user's point of view, a monitor gone before the stop
			// reached it is a done stop.
			return s.settleUnreachableMonitor(ctx, st, id, cfg, nil)
		}
		return false, err
	}
	if err := waitForStopped(ctx, st, id, timeout); err == nil {
		return false, nil
	} else if !errors.Is(err, context.DeadlineExceeded) {
		return false, err
	}

	// The monitor escalates to SIGKILL on its own at the same deadline. This
	// SIGKILL stays anyway: a duplicate SIGKILL is harmless, and sending it here
	// keeps the wait below and its error reporting on the client's own clock.
	killSig, _, _ := hrstr.ParseSignal("SIGKILL")
	if err := s.sendStop(ctx, st, id, killSig, 0); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// A command that removes itself is gone from the store once the
			// monitor's own SIGKILL ended its run, which is the stop having
			// succeeded.
			return false, nil
		}
		killErr := fmt.Errorf("timeout waiting for stop, and SIGKILL failed: %w", err)
		if isMonitorUnavailable(err) {
			// The client's SIGKILL never went out, and a monitor that died short
			// of recording the end of the run may never have sent its own, so
			// whatever ignored the stop's signal may still be running.
			return s.settleUnreachableMonitor(ctx, st, id, cfg, killErr)
		}
		return false, killErr
	}
	if err := waitForStopped(ctx, st, id, timeout); err != nil {
		return false, fmt.Errorf("timeout waiting for stop after SIGKILL: %w", err)
	}
	return false, nil
}

// settleUnreachableMonitor decides what a monitor the stop could not reach
// means. The state read before the connect can be stale by then: the command
// may have exited on its own in between, and the monitor escalates a stop at
// the same deadline as the client, so the run its SIGKILL ends often takes the
// monitor down before the client's own SIGKILL connects. The monitor records
// the terminal state before it closes its socket, so the state is read again
// here, and a terminal state or a removed command is a stop that succeeded.
// Marking that command failed would turn a clean end into a dead monitor.
//
// Anything else is a monitor that died on the way. Its death is recorded on top
// of the state just read, not the stale one, so nothing the monitor wrote since
// is lost. The caller passes what the stop reports in that case as retErr. A
// nil retErr means the monitor's death alone settles the stop. died reports
// that case, a death stale cleanup recorded included.
func (s *Service) settleUnreachableMonitor(
	ctx context.Context,
	st *store.Store,
	id string,
	cfg *model.CommandConfig,
	retErr error,
) (died bool, err error) {
	state, _, stateJSON, err := st.GetCommandState(id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.Join(retErr, fmt.Errorf("get command state: %w", err))
	}
	if monitor.DiedUnexpectedly(stateJSON) {
		// Stale cleanup got here first. Its failed state records the monitor's
		// death, not the end of the run, so whatever ignored the stop's signal may
		// still be running.
		return true, retErr
	}
	if state == model.EventTypeExited || state == model.EventTypeFailed {
		return false, nil
	}
	if err := monitor.MarkMonitorDied(ctx, st, s.cfg, id, stateJSON, cfg); err != nil {
		return true, errors.Join(retErr, err)
	}
	return true, retErr
}

func (s *Service) sendStop(
	ctx context.Context,
	st *store.Store,
	id string,
	sig int32,
	timeout time.Duration,
) error {
	_, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return err
	}

	conn, err := s.connectMonitor(ctx, stateJSON)
	if err != nil {
		return fmt.Errorf("%q: %w", id, err)
	}
	defer conn.Close()

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	req := &cmdmanv1pb.StopRequest{Signal: sig}
	if timeout > 0 {
		req.Timeout = durationpb.New(timeout)
	}
	_, err = client.Stop(ctx, req)
	return err
}

func waitForStopped(ctx context.Context, st *store.Store, id string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		state, _, _, err := st.GetCommandState(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if state == model.EventTypeExited || state == model.EventTypeFailed {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}

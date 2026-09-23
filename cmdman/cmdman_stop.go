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
		timeout = 10 * time.Second
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

func (s *Service) stop(
	ctx context.Context,
	st *store.Store,
	id string,
	signalOverride string,
	timeout time.Duration,
) error {
	state, _, stateJSON, err := st.GetCommandState(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get command state: %w", err)
	}
	if state == model.EventTypeExited || state == model.EventTypeFailed {
		return nil
	}

	_, _, cfg, err := st.GetCommandConfig(id)
	if err != nil {
		return fmt.Errorf("get command config: %w", err)
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
		return err
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
			return monitor.MarkMonitorDied(ctx, st, s.cfg, id, stateJSON, cfg)
		}
		return err
	}
	if err := waitForStopped(ctx, st, id, timeout); err == nil {
		return nil
	} else if !errors.Is(err, context.DeadlineExceeded) {
		return err
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
			return nil
		}
		killErr := fmt.Errorf("timeout waiting for stop, and SIGKILL failed: %w", err)
		if isMonitorUnavailable(err) {
			return s.settleUnreachableKill(ctx, st, id, cfg, killErr)
		}
		return killErr
	}
	if err := waitForStopped(ctx, st, id, timeout); err != nil {
		return fmt.Errorf("timeout waiting for stop after SIGKILL: %w", err)
	}
	return nil
}

// settleUnreachableKill decides what a monitor that could not be reached with
// the stop's SIGKILL means. The monitor escalates at the same deadline as the
// client, and the run that SIGKILL ends takes the monitor down with it, so the
// monitor is often gone by the time the client's own SIGKILL connects. The
// monitor records the terminal state before it closes its socket, so a
// terminal state here is a stop that succeeded.
//
// Anything else is a monitor that died on the way. Its death is recorded, but
// the stop still reports killErr: the client's SIGKILL never went out, and a
// monitor that died short of recording the end of the run may never have sent
// its own, so whatever ignored the stop's signal may still be running.
func (s *Service) settleUnreachableKill(
	ctx context.Context,
	st *store.Store,
	id string,
	cfg *model.CommandConfig,
	killErr error,
) error {
	state, _, stateJSON, err := st.GetCommandState(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Join(killErr, fmt.Errorf("get command state: %w", err))
	}
	if state == model.EventTypeExited || state == model.EventTypeFailed {
		return nil
	}
	if err := monitor.MarkMonitorDied(ctx, st, s.cfg, id, stateJSON, cfg); err != nil {
		return errors.Join(killErr, err)
	}
	return killErr
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

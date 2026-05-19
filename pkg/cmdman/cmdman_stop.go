package cmdman

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/eventlog"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
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
	_, _, cfg, err := st.GetCommandConfig(id)
	if err != nil {
		return fmt.Errorf("get command config: %w", err)
	}

	effective := cfg.StopSignal
	if signalOverride != "" {
		effective = signalOverride
	}
	if effective == "" {
		effective = store.DefaultStopSignal
	}
	sig, _, err := store.ParseSignal(effective)
	if err != nil {
		return err
	}

	s.emitEvent(eventlog.Event{
		Time: time.Now().UTC(),
		Type: eventlog.EventTypeStop,
		ID:   id,
		Attrs: map[string]string{
			"signal": fmt.Sprintf("%d", sig),
		},
	})

	if err := s.sendStop(ctx, st, id, sig); err != nil {
		return err
	}
	if err := waitForStopped(ctx, st, id, timeout); err == nil {
		return nil
	} else if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	killSig, _, _ := store.ParseSignal("SIGKILL")
	if err := s.sendStop(ctx, st, id, killSig); err != nil {
		return fmt.Errorf("timeout waiting for stop, and SIGKILL failed: %w", err)
	}
	if err := waitForStopped(ctx, st, id, timeout); err != nil {
		return fmt.Errorf("timeout waiting for stop after SIGKILL: %w", err)
	}
	return nil
}

func (s *Service) sendStop(ctx context.Context, st *store.Store, id string, sig int32) error {
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
	_, err = client.Stop(ctx, &cmdmanv1pb.StopRequest{Signal: sig})
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
		if state == store.StateExited || state == store.StateFailed {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}

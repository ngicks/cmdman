package cmdman

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/cmdman/pkg/hrstr"
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

	s.emitEvent(model.Event{
		Time: time.Now().UTC(),
		Type: model.EventTypeStopped,
		ID:   id,
		Attrs: map[string]string{
			"signal": fmt.Sprintf("%d", sig),
		},
	})

	if err := s.sendStop(ctx, st, id, sig); err != nil {
		if isMonitorUnavailable(err) {
			return markMonitorDied(st, s.cfg, id, stateJSON, cfg)
		}
		return err
	}
	if err := waitForStopped(ctx, st, id, timeout); err == nil {
		return nil
	} else if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	killSig, _, _ := hrstr.ParseSignal("SIGKILL")
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

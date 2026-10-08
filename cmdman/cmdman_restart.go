package cmdman

import (
	"context"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// RestartRequest defines a restart operation across explicit targets.
// Restart is the equivalent of running Stop followed by Start on each target.
type RestartRequest struct {
	Targets []string
	Signal  string
	// Timeout is [StopRequest.Timeout] for the stop phase.
	Timeout *time.Duration
}

type RestartResult struct {
	ID  string
	Err error
	// ForceKilled is [StopResult.ForceKilled] for the stop phase.
	ForceKilled bool
}

func (s *Service) Restart(ctx context.Context, req RestartRequest) ([]RestartResult, error) {
	if err := checkStopTimeout(req.Timeout); err != nil {
		return nil, err
	}
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	ids, err := resolveTargets(st, req.Targets, nil)
	if err != nil {
		return nil, err
	}

	results := make([]RestartResult, 0, len(ids))
	for _, id := range ids {
		forceKilled, err := s.restart(ctx, st, id, req.Signal, req.Timeout)
		results = append(results, RestartResult{ID: id, Err: err, ForceKilled: forceKilled})
	}
	return results, nil
}

func (s *Service) restart(
	ctx context.Context,
	st *store.Store,
	id string,
	signalOverride string,
	timeoutOverride *time.Duration,
) (forceKilled bool, err error) {
	state, _, _, err := st.GetCommandState(id)
	if err != nil {
		return false, fmt.Errorf("get command state: %w", err)
	}
	if state == model.EventTypeStarting || state == model.EventTypeRunning {
		forceKilled, err = s.stop(ctx, st, id, signalOverride, timeoutOverride)
		if err != nil {
			return forceKilled, fmt.Errorf("stop: %w", err)
		}
	}
	if err := s.Start(ctx, id); err != nil {
		return forceKilled, fmt.Errorf("start: %w", err)
	}
	return forceKilled, nil
}

package cmdman

import (
	"context"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// RestartRequest defines a restart operation across explicit targets.
// Restart is the equivalent of running Stop followed by Start on each target.
type RestartRequest struct {
	Targets []string
	Signal  string
	Timeout time.Duration
}

type RestartResult struct {
	ID  string
	Err error
}

func (s *Service) Restart(ctx context.Context, req RestartRequest) ([]RestartResult, error) {
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
	results := make([]RestartResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, RestartResult{
			ID:  id,
			Err: s.restart(ctx, st, id, req.Signal, timeout),
		})
	}
	return results, nil
}

func (s *Service) restart(
	ctx context.Context,
	st *store.Store,
	id string,
	signalOverride string,
	timeout time.Duration,
) error {
	state, _, _, err := st.GetCommandState(id)
	if err != nil {
		return fmt.Errorf("get command state: %w", err)
	}
	if state == store.StateStarting || state == store.StateRunning {
		if err := s.stop(ctx, st, id, signalOverride, timeout); err != nil {
			return fmt.Errorf("stop: %w", err)
		}
	}
	if err := s.Start(ctx, id); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	return nil
}

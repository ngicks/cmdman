package cmdman

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// Wait blocks until each target reaches req.Condition (default "stopped",
// matching either StateExited or StateFailed), then returns one WaitResult
// per target in argument order. A target removed from the store while we
// poll is treated as terminal. With Ignore=true, targets that fail to
// resolve are skipped silently instead of being reported.
func (s *Service) Wait(ctx context.Context, req WaitRequest) ([]WaitResult, error) {
	condition := req.Condition
	if condition == "" {
		condition = WaitConditionStopped
	}
	if !validWaitCondition(condition) {
		return nil, fmt.Errorf("invalid wait condition %q", condition)
	}
	interval := req.Interval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}

	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	results := make([]WaitResult, 0, len(req.Targets))
	for _, target := range req.Targets {
		id, err := st.ResolveID(target)
		if err != nil {
			if req.Ignore {
				continue
			}
			results = append(results, WaitResult{
				ID:  target,
				Err: fmt.Errorf("resolve %q: %w", target, err),
			})
			continue
		}
		exitCode, err := waitForCondition(ctx, st, id, condition, interval)
		results = append(results, WaitResult{ID: id, ExitCode: exitCode, Err: err})
	}
	return results, nil
}

func validWaitCondition(c string) bool {
	switch c {
	case WaitConditionStopped, WaitConditionCreated, WaitConditionStarting,
		WaitConditionRunning, WaitConditionExited, WaitConditionFailed:
		return true
	}
	return false
}

func matchesWaitCondition(state, condition string) bool {
	if condition == WaitConditionStopped {
		return state == store.StateExited || state == store.StateFailed
	}
	return state == condition
}

func waitForCondition(
	ctx context.Context,
	st *store.Store,
	id, condition string,
	interval time.Duration,
) (*int, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		state, exitCode, _, err := st.GetCommandState(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if matchesWaitCondition(state, condition) {
			return exitCode, nil
		}
		select {
		case <-ctx.Done():
			return exitCode, ctx.Err()
		case <-ticker.C:
		}
	}
}

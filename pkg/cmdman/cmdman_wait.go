package cmdman

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/eventlog"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// WaitRequest defines a wait operation across explicit targets.
type WaitRequest struct {
	Targets   []string
	Condition string
	Interval  time.Duration
	Ignore    bool
}

// WaitResult reports per-command outcome of a Wait operation.
// ExitCode is nil when the command has not exited (e.g. when waiting for a
// non-terminal condition such as "running") or when the command has been
// removed from the store before any exit code was recorded.
type WaitResult struct {
	ID       string
	ExitCode *int
	Err      error
}

// Wait conditions accepted by Service.Wait. "stopped" is satisfied by either
// "exited" or "failed" states; the rest match the corresponding state
// verbatim.
const (
	WaitConditionStopped  = "stopped"
	WaitConditionCreated  = "created"
	WaitConditionStarting = "starting"
	WaitConditionRunning  = "running"
	WaitConditionExited   = "exited"
	WaitConditionFailed   = "failed"
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
		exitCode, err := s.waitForCondition(ctx, st, id, condition, interval)
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

// waitForCondition blocks until id reaches condition. It combines an
// event-log subscription (for prompt wake-ups when the monitor publishes
// a state-change event) with periodic polling of CommandState (as a
// fallback when the event log is missing or the publisher is slow).
func (s *Service) waitForCondition(
	ctx context.Context,
	st *store.Store,
	id, condition string,
	interval time.Duration,
) (*int, error) {
	// Subscribe to events filtered by id BEFORE the first state read so
	// we don't miss an event that fires between the initial read and the
	// subscription opening.
	var events <-chan eventlog.Record
	if sub, err := s.Events(ctx, EventsRequest{
		Follow:   true,
		IDFilter: []string{id},
		FromEnd:  true,
	}); err == nil {
		defer sub.Close()
		events = sub.Records()
	}

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
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		}
	}
}

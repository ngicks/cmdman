package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// WaitOption configures a Wait operation.
type WaitOption struct {
	// Targets optionally narrows the target set to specific compose commands or
	// replicas. Empty targets the whole project. A replica index must name a
	// stored replica.
	Targets []Target
	// Condition is the wait condition (default "stopped").
	// Valid values: "stopped", "created", "starting", "running", "exited", "failed".
	Condition model.EventType
	// Interval is the polling interval (default: 250ms when zero).
	Interval time.Duration
	// Ignore causes targets that fail to resolve to be skipped silently.
	Ignore bool
}

// WaitResult is the aggregated result of a compose wait operation.
type WaitResult struct {
	Outcomes []WaitOutcome
}

// WaitOutcome records the result of waiting for a single replica of a compose
// command.
type WaitOutcome struct {
	// Command labels the replica: the bare command name for an unscaled command,
	// "<command>-<index>" for a scaled one.
	Command  string
	ExitCode *int
	Err      error
}

// Wait blocks until each selected command reaches the specified condition.
//
// The default condition is "stopped" (satisfied by either "exited" or "failed").
// A single call to cmdman.Service.Wait handles all targets concurrently.
//
// Per resolved-decision 15, an empty project target set exits 0 with a
// structured log event. Per resolved-decision 21, per-target errors are
// aggregated in the returned result.
func (s *Service) Wait(
	ctx context.Context,
	selection ProjectSelection,
	opts WaitOption,
) (*WaitResult, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	targets, err := resolveTargets(opts.Targets, storedReplicas(selection.Spec, entries))
	if err != nil {
		return nil, err
	}
	nameOf := replicaNamer(selection.Spec, entries)
	entries = targets.filter(entries)

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose wait: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "wait",
		)
		return &WaitResult{}, nil
	}

	// Build ID list and a reverse map from ID → replica label.
	ids := make([]string, 0, len(entries))
	nameByID := make(map[string]string, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
		nameByID[e.ID] = nameOf(e)
	}

	condition := opts.Condition
	if condition == "" {
		condition = cmdman.WaitConditionStopped
	}

	results, err := s.svc.Wait(ctx, cmdman.WaitRequest{
		Targets:   ids,
		Condition: condition,
		Interval:  opts.Interval,
		Ignore:    opts.Ignore,
	})
	if err != nil {
		return nil, fmt.Errorf("wait: %w", err)
	}

	outcomes := make([]WaitOutcome, 0, len(results))
	for _, r := range results {
		name := nameByID[r.ID]
		if name == "" {
			name = r.ID // fall back to raw ID if label is absent
		}
		outcomes = append(outcomes, WaitOutcome{
			Command:  name,
			ExitCode: r.ExitCode,
			Err:      r.Err,
		})
	}

	return &WaitResult{Outcomes: outcomes}, nil
}

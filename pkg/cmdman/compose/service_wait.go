package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

// WaitOption configures a Wait operation.
type WaitOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
	// Condition is the wait condition (default "stopped").
	// Valid values: "stopped", "created", "starting", "running", "exited", "failed".
	Condition string
	// Interval is the polling interval (default: 250ms when zero).
	Interval time.Duration
	// Ignore causes targets that fail to resolve to be skipped silently.
	Ignore bool
}

// WaitResult is the aggregated result of a compose wait operation.
type WaitResult struct {
	Outcomes []WaitOutcome
}

// WaitOutcome records the result of waiting for a single compose command.
type WaitOutcome struct {
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

	if err := validateCommandNames(opts.CommandNames, selection.Spec, entries); err != nil {
		return nil, err
	}
	if len(opts.CommandNames) > 0 {
		entries = filterByCommandNames(entries, opts.CommandNames)
	}

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose wait: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "wait",
		)
		return &WaitResult{}, nil
	}

	// Build ID list and a reverse map from ID → compose command name.
	ids := make([]string, 0, len(entries))
	nameByID := make(map[string]string, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
		name := ""
		if e.ConfigJSON != nil {
			name = e.ConfigJSON.Labels[LabelCommand]
		}
		nameByID[e.ID] = name
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

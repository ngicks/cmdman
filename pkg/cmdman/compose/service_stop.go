package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

// StopOption configures a Stop operation.
type StopOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
}

// StopResult is the aggregated result of a compose stop operation.
type StopResult struct {
	Stops []StopOutcome
}

// StopOutcome records the result of stopping a single compose command.
type StopOutcome struct {
	Command string
	Err     error
}

// Stop stops project-labeled commands.
//
// When selection carries a Spec (compose file loaded), commands are stopped by
// an up walk of the reconcile graph: dependents are stopped before the
// dependencies they rely on, and independent branches stop concurrently. When
// command names are given, their recursive dependents are pulled in so a
// dependency is never stopped while a command that depends on it is still
// running.
//
// When no Spec is loaded, the dependency graph is reconstructed from stored
// compose labels.
//
// An empty resolved target set is not an error: the caller should emit a
// structured-log event and return nil.
//
// Per resolved-decision 21, failures are aggregated; every command is attempted.
func (s *Service) Stop(
	ctx context.Context,
	selection ProjectSelection,
	opts StopOption,
) (*StopResult, error) {
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

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose stop: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "stop",
		)
		return &StopResult{}, nil
	}

	var stops []StopOutcome
	if selection.Spec != nil {
		stops, err = s.reconcileStop(ctx, *selection.Spec, opts.CommandNames)
		if err != nil {
			return nil, err
		}
	} else {
		spec, ok, err := reconstructProjectFromMeta(selection, entries)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf(
				"compose stop: stored dependency graph is ambiguous; pass -f or --project-name",
			)
		}
		stops, err = s.reconcileStop(ctx, spec, opts.CommandNames)
		if err != nil {
			return nil, err
		}
	}

	return &StopResult{Stops: stops}, nil
}

// stopAllConcurrent stops all entries concurrently and returns outcomes.
func stopAllConcurrent(
	ctx context.Context,
	s *Service,
	entries []cmdmanEntry,
	project string,
) []StopOutcome {
	var (
		mu       sync.Mutex
		outcomes []StopOutcome
	)
	eg, _ := errgroup.WithContext(ctx)

	for _, entry := range entries {
		// Label replicas by scale index so a scaled command shows every replica
		// rather than collapsing them under the bare command name.
		name := entryDisplayName(entry)
		id := entry.ID
		eg.Go(func() error {
			s.report(name, PhaseStopping, nil, nil)
			_, err := s.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}})
			outcome := StopOutcome{Command: name}
			if err != nil {
				outcome.Err = fmt.Errorf("stop command %q (%s): %w", name, id, err)
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose stop: stop failed",
					"project", project,
					"command", name,
					"id", id,
					"error", err,
				)
				s.report(name, PhaseError, outcome.Err, nil)
			} else {
				s.report(name, PhaseStopped, nil, nil)
			}
			mu.Lock()
			outcomes = append(outcomes, outcome)
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()
	return outcomes
}

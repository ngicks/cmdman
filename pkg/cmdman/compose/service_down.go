package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// DownOption configures a Down operation.
type DownOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
}

// DownResult is the aggregated result of a compose down operation.
type DownResult struct {
	Stops   []StopOutcome
	Removes []RemoveOutcome
}

// RemoveOutcome records the result of removing a single compose command.
type RemoveOutcome struct {
	Command string
	Err     error
}

// Down stops and then removes project-labeled commands.
//
// Stop phase: same ordering as Stop (reverse-dependency up walk when Spec is
// loaded; concurrent otherwise). Remove phase: fully concurrent after all stops
// complete.
//
// With no command names and a loaded Spec, Down is the destructive whole-project
// teardown: because selection is by the (workdir, project) label pair, it also
// stops and removes orphans of that pair (resolved-decision 20). With command
// names, the target set is the named commands plus their recursive dependents;
// only that set is stopped and removed.
//
// Per resolved-decision 21, failures are aggregated; every command is attempted.
func (s *Service) Down(
	ctx context.Context,
	selection ProjectSelection,
	opts DownOption,
) (*DownResult, error) {
	allEntries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	if err := validateCommandNames(opts.CommandNames, selection.Spec, allEntries); err != nil {
		return nil, err
	}

	selected := allEntries
	if len(opts.CommandNames) > 0 {
		selected = filterByCommandNames(allEntries, opts.CommandNames)
	}
	if len(selected) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose down: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "down",
		)
		return &DownResult{}, nil
	}

	var (
		stops         []StopOutcome
		removeTargets []cmdmanEntry
	)
	if selection.Spec != nil {
		// Stop the declared closure (named + recursive dependents) in
		// reverse-dependency order via the reconcile graph.
		stops, err = s.reconcileStop(ctx, *selection.Spec, opts.CommandNames)
		if err != nil {
			return nil, err
		}
		if len(opts.CommandNames) == 0 {
			// Whole-project teardown: also stop running orphans, remove everything.
			stops = append(
				stops,
				s.stopOrphans(ctx, allEntries, *selection.Spec, selection.Project)...)
			removeTargets = allEntries
		} else {
			// Scoped teardown: remove exactly the stopped closure.
			closure := resolveStopTargetCommands(*selection.Spec, opts.CommandNames)
			removeTargets = filterEntriesInClosure(allEntries, closure)
		}
	} else {
		// No spec: stop running entries concurrently, remove the selected set.
		stops = stopAllConcurrent(ctx, s, runningEntries(selected), selection.Project)
		removeTargets = selected
	}

	// Remove phase: fully concurrent, regardless of stop errors.
	removes := removeAllConcurrent(ctx, s, removeTargets, selection.Project)

	return &DownResult{Stops: stops, Removes: removes}, nil
}

// runningEntries returns the entries with a live monitor (started/starting),
// the only states Service.Stop can gracefully stop.
func runningEntries(entries []cmdmanEntry) []cmdmanEntry {
	out := make([]cmdmanEntry, 0, len(entries))
	for _, e := range entries {
		if e.State == model.EventTypeStarted || e.State == model.EventTypeStarting {
			out = append(out, e)
		}
	}
	return out
}

// stopOrphans stops running project entries whose command is not declared in the
// spec. Down is a destructive whole-project teardown, so orphans are torn down
// alongside declared commands.
func (s *Service) stopOrphans(
	ctx context.Context,
	entries []cmdmanEntry,
	spec ComposeSpec,
	project string,
) []StopOutcome {
	declared := make(map[string]struct{}, len(spec.Commands))
	for _, nc := range spec.Commands {
		declared[nc.Name] = struct{}{}
	}
	var orphans []cmdmanEntry
	for _, e := range runningEntries(entries) {
		if e.ConfigJSON == nil {
			continue
		}
		if _, ok := declared[e.ConfigJSON.Labels[LabelCommand]]; !ok {
			orphans = append(orphans, e)
		}
	}
	if len(orphans) == 0 {
		return nil
	}
	return stopAllConcurrent(ctx, s, orphans, project)
}

// filterEntriesInClosure returns the entries whose compose command name is a
// member of closure.
func filterEntriesInClosure(entries []cmdmanEntry, closure map[string]struct{}) []cmdmanEntry {
	out := make([]cmdmanEntry, 0, len(entries))
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		if _, ok := closure[e.ConfigJSON.Labels[LabelCommand]]; ok {
			out = append(out, e)
		}
	}
	return out
}

// removeAllConcurrent removes all entries concurrently and returns outcomes.
func removeAllConcurrent(
	ctx context.Context,
	s *Service,
	entries []cmdmanEntry,
	project string,
) []RemoveOutcome {
	var (
		mu       sync.Mutex
		outcomes []RemoveOutcome
	)
	eg, _ := errgroup.WithContext(ctx)

	for _, entry := range entries {
		cmdName := ""
		if entry.ConfigJSON != nil {
			cmdName = entry.ConfigJSON.Labels[LabelCommand]
		}
		id := entry.ID
		name := cmdName
		eg.Go(func() error {
			results, err := s.svc.Remove(ctx, cmdman.RemoveRequest{
				Targets: []string{id},
				Force:   true,
			})
			outcome := RemoveOutcome{Command: name}
			if err != nil {
				outcome.Err = fmt.Errorf("remove command %q (%s): %w", name, id, err)
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose down: remove failed",
					"project", project,
					"command", name,
					"id", id,
					"error", err,
				)
			} else {
				for _, r := range results {
					if r.Err != nil {
						outcome.Err = fmt.Errorf("remove command %q (%s): %w", name, id, r.Err)
						contextkey.ValueSlogLoggerDefault(ctx).Warn("compose down: remove failed",
							"project", project,
							"command", name,
							"id", id,
							"error", r.Err,
						)
						break
					}
				}
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

package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
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

// Down stops and then removes all project-labeled commands.
//
// Stop phase: same ordering as Stop (reverse DAG when Spec is loaded; concurrent otherwise).
// Remove phase: fully concurrent after all stops complete.
//
// Because selection is by the (workdir, project) label pair, Down implicitly
// removes orphans of that pair. This is intentional: down is the destructive
// whole-project teardown (resolved-decision 20).
//
// Per resolved-decision 21, failures are aggregated; every command is attempted.
func (s *Service) Down(
	ctx context.Context,
	selection ProjectSelection,
	opts DownOption,
) (*DownResult, error) {
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
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose down: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "down",
		)
		return &DownResult{}, nil
	}

	// Stop phase. Only entries with a live monitor (running/starting) can be
	// gracefully stopped; created/exited/failed entries are no-ops and would
	// otherwise return monitor-connect errors from Service.Stop.
	stoppable := make([]cmdmanEntry, 0, len(entries))
	for _, e := range entries {
		if e.State == store.StateRunning || e.State == store.StateStarting {
			stoppable = append(stoppable, e)
		}
	}

	var stops []StopOutcome
	if selection.Spec != nil {
		layers, err := TopoLayers(selection.Spec.Commands)
		if err != nil {
			return nil, fmt.Errorf("topo layers: %w", err)
		}
		reverseLayers(layers)
		idByCommand := buildIDByCommand(stoppable)
		for _, layer := range layers {
			outcomes := stopLayerConcurrent(ctx, s, layer, idByCommand, selection.Project)
			stops = append(stops, outcomes...)
		}
		// Also stop entries not in the YAML (orphans) — down is destructive.
		yamlNames := make(map[string]struct{}, len(selection.Spec.Commands))
		for _, nc := range selection.Spec.Commands {
			yamlNames[nc.Name] = struct{}{}
		}
		var orphanEntries []cmdmanEntry
		for _, e := range stoppable {
			if e.ConfigJSON == nil {
				continue
			}
			cn := e.ConfigJSON.Labels[LabelCommand]
			if _, inYAML := yamlNames[cn]; !inYAML {
				orphanEntries = append(orphanEntries, e)
			}
		}
		if len(orphanEntries) > 0 {
			orphanStops := stopAllConcurrent(ctx, s, orphanEntries, selection.Project)
			stops = append(stops, orphanStops...)
		}
	} else {
		stops = stopAllConcurrent(ctx, s, stoppable, selection.Project)
	}

	// Remove phase: fully concurrent, regardless of stop errors.
	removes := removeAllConcurrent(ctx, s, entries, selection.Project)

	return &DownResult{Stops: stops, Removes: removes}, nil
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

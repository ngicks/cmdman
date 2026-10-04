package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// DownOption configures a Down operation.
type DownOption struct {
	// Targets optionally narrows the teardown to specific compose commands.
	// Empty targets the whole project. A target selecting a single replica is
	// rejected: see [Service.Down].
	Targets []Target
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
// Stop phase: same ordering as Stop (reverse-dependency up walk). Remove
// phase: fully concurrent after all stops complete.
//
// With no targets and a loaded Spec, Down is the destructive whole-project
// teardown: because selection is by the (workdir, project) label pair, it also
// stops and removes orphans of that pair (resolved-decision 20). With targets,
// the target set is the targeted commands plus their recursive dependents; only
// that set is stopped and removed.
//
// Down removes whole commands only. Removing one replica would leave a gap in
// the command's scale indices, and both the fileless project reconstruction and
// the start waits assume replicas are numbered 1..N without gaps. A target with
// a non-zero ScaleIndex is therefore an error; scaling the command down
// ([Service.Scale]) is how replicas are removed.
//
// Per resolved-decision 21, failures are aggregated; every command is attempted.
func (s *Service) Down(
	ctx context.Context,
	selection ProjectSelection,
	opts DownOption,
) (*DownResult, error) {
	for _, t := range opts.Targets {
		if t.ScaleIndex != 0 {
			return nil, fmt.Errorf(
				"compose down: cannot remove replica %d of %q alone; scale the command down instead",
				t.ScaleIndex,
				t.Command,
			)
		}
	}

	allEntries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	targets, err := resolveTargets(opts.Targets, storedReplicas(selection.Spec, allEntries))
	if err != nil {
		return nil, err
	}

	spec := selection.Spec
	if spec == nil && !spansMultipleProjects(allEntries) {
		stored, ok, err := reconstructProjectFromMeta(selection, allEntries)
		if err != nil {
			return nil, err
		}
		if !ok && len(allEntries) > 0 {
			return nil, fmt.Errorf(
				"compose down: stored dependency graph is ambiguous; pass -f or --project-name",
			)
		}
		if ok {
			spec = &stored
		}
	}

	selected := targets.filter(allEntries)
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
	if spec != nil {
		// Stop the declared closure (named + recursive dependents) in
		// reverse-dependency order via the reconcile graph.
		stops, err = s.reconcileStop(ctx, *spec, targets)
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			// Whole-project teardown: also stop running orphans, remove everything.
			stops = append(
				stops, s.stopOrphans(ctx, allEntries, *spec, selection.Project)...)
			removeTargets = allEntries
		} else {
			// Scoped teardown: remove exactly the stopped closure.
			closure := resolveStopTargetCommands(*spec, targets.names())
			removeTargets = filterEntriesInClosure(allEntries, closure)
		}
	} else {
		// No reconstructable graph: stop running entries concurrently, remove
		// the selected set.
		stops = stopAllConcurrent(ctx, s, runningEntries(selected), selection.Project)
		removeTargets = selected
	}

	removes := removeAllConcurrent(ctx, s, removeTargets, selection.Project)

	return &DownResult{Stops: stops, Removes: removes}, nil
}

// spansMultipleProjects reports whether entries carry more than one distinct
// compose project label.
//
// A cwd-scoped selection (no -f and no --project-name) legitimately matches
// every project sharing the workdir, so the entries can belong to several
// projects. There is no single dependency graph across projects, so Down must
// not treat that as the ambiguous-graph error; instead it skips graph
// reconstruction and tears every selected command down via the brute-force
// stop+remove path (spec stays nil). Per-project reverse-dependency stop
// ordering is not preserved across that whole-workdir teardown, which matches
// the fileless behavior before graph reconstruction was introduced.
func spansMultipleProjects(entries []cmdmanEntry) bool {
	first := ""
	seen := false
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		project := e.ConfigJSON.Labels[LabelProject]
		if !seen {
			first = project
			seen = true
			continue
		}
		if project != first {
			return true
		}
	}
	return false
}

// runningEntries returns the entries with a live monitor (running/starting),
// the only states Service.Stop can gracefully stop.
func runningEntries(entries []cmdmanEntry) []cmdmanEntry {
	out := make([]cmdmanEntry, 0, len(entries))
	for _, e := range entries {
		if e.State == model.EventTypeRunning || e.State == model.EventTypeStarting {
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
		// Label replicas by scale index so a scaled command shows every replica
		// rather than collapsing them under the bare command name.
		name := entryDisplayName(entry)
		id := entry.ID
		eg.Go(func() error {
			s.report(name, PhaseRemoving, nil, nil)
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
			if outcome.Err != nil {
				s.report(name, PhaseError, outcome.Err, nil)
			} else {
				s.report(name, PhaseRemoved, nil, nil)
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

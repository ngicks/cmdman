package compose

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

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
	// Force lets every hook that fails under on_error fail pass as on_error
	// continue: the failure is reported as a warning, and the replica is
	// stopped and removed all the same. A replica whose stored hooks cannot be
	// decoded is stopped and removed without them, with a warning. The stored
	// releases a whole-project down runs are forced the same way.
	Force bool
	// Timeout is how long the stop of each replica waits after the stop signal
	// before it sends SIGKILL. Nil waits each replica's stored stop timeout. A
	// non-positive value fails the down before any replica is stopped.
	Timeout *time.Duration
}

// DownResult is the aggregated result of a compose down operation.
type DownResult struct {
	Stops   []StopOutcome
	Removes []RemoveOutcome
	// Releases are the stored releases a whole-project down ran for the
	// resources whose replica was already gone, and for the stop releases of
	// the replicas it removed without stopping them.
	Releases []ReleaseOutcome
}

// RemoveOutcome records the result of removing a single compose command.
type RemoveOutcome struct {
	Command string
	Err     error
}

// ReleaseOutcome records the result of running the stored release of one
// resource whose replica is gone.
type ReleaseOutcome struct {
	// Holder is the cmdman command name of the resource holder. It is empty
	// when the holders could not be listed: Err is that failure.
	Holder string
	Err    error
}

// Down stops and then removes project-labeled commands.
//
// Stop phase: same ordering as Stop (reverse-dependency up walk). Every live
// replica stops inside the stop hooks stored on it. Remove phase: fully
// concurrent after all stops complete. Every replica is removed inside the
// remove hooks stored on it, and the exec commands its failed hooks left for
// inspection are removed with it.
//
// A replica whose stop_pre or stop_post failed under on_error fail is kept: it
// is not removed, and its RemoveOutcome carries the failure. A replica whose
// stop failed for any other reason is removed by force. A remove_pre that
// fails under on_error fail keeps its replica too, and so do stored hooks that
// cannot be decoded. opts.Force lets every failing hook pass as on_error
// continue instead, and tears a replica with undecodable hooks down without
// them.
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
// A whole-project down then releases the resources of the project left behind
// by a replica that no longer existed when Down began, and the resources with a
// stop_pre or stop_post release of a replica Down removed without stopping it,
// such as one whose command had exited on its own: the release stored with
// each runs as the release hook would have, under the on_error stored with it,
// and the holder goes once the release succeeds. These run after the remove
// hooks of every replica. A release that the hooks of a replica ran in this
// down and that failed waits for the next down. A holder that stores no release stays. This needs
// no compose
// file. A failure to list the holders becomes a failed ReleaseOutcome. Down
// returns no error for it, so the outcomes of the replicas already torn down
// still reach the caller.
//
// Per resolved-decision 21, failures are aggregated; every command is attempted.
func (s *Service) Down(
	ctx context.Context,
	selection ProjectSelection,
	opts DownOption,
) (*DownResult, error) {
	if err := checkStopTimeout(opts.Timeout); err != nil {
		return nil, err
	}
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

	result := &DownResult{}
	td := &teardown{force: opts.Force, timeout: opts.Timeout}
	selected := targets.filter(allEntries)
	if len(selected) > 0 {
		var removeTargets []cmdmanEntry
		if spec != nil {
			// Stop the declared closure (named + recursive dependents) in
			// reverse-dependency order via the reconcile graph.
			result.Stops, err = s.reconcileStop(ctx, *spec, targets, td)
			if err != nil {
				return nil, err
			}
			if len(targets) == 0 {
				// Whole-project teardown: also stop running orphans, remove everything.
				result.Stops = append(result.Stops,
					s.stopOrphans(ctx, allEntries, *spec, selection.Project, td)...)
				removeTargets = allEntries
			} else {
				// Scoped teardown: remove exactly the stopped closure.
				closure := resolveStopTargetCommands(*spec, targets.names())
				removeTargets = filterEntriesInClosure(allEntries, closure)
			}
		} else {
			// No reconstructable graph: stop running entries concurrently, remove
			// the selected set.
			result.Stops = stopAllConcurrent(
				ctx, s, runningEntries(selected), selection.Project, td)
			removeTargets = selected
		}
		result.Removes = removeAllConcurrent(ctx, s, removeTargets, selection.Project, td)
	}

	if len(targets) == 0 {
		result.Releases = s.releaseStranded(ctx, selection, allEntries, td)
	}

	if len(selected) == 0 && len(result.Releases) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose down: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "down",
		)
	}
	return result, nil
}

// releaseStranded runs the stored release of every resource of selection whose
// replica is gone, unless td has run that release. Such a replica is either not
// among replicas, the project's replicas as Down found them before tearing any
// down, or one td removed without running the stop release of the resource
// ([teardown.releaseLeft]). A release td ran and that failed waits for the next
// down, so no release runs twice in one down. A holder that stores no release
// is left alone, and so is one that cannot be read. A failure to list the
// holders is the one outcome returned.
func (s *Service) releaseStranded(
	ctx context.Context,
	selection ProjectSelection,
	replicas []cmdmanEntry,
	td *teardown,
) []ReleaseOutcome {
	logger := contextkey.ValueSlogLoggerDefault(ctx)
	labels := hooksProjectLabels(selection.WorkDir, selection.Project)
	labels[LabelIntermediate] = IntermediateHolder
	entries, err := s.svc.List(ctx, cmdman.ListRequest{AllStates: true, Labels: labels})
	if err != nil {
		err = fmt.Errorf("compose down: list resource holders: %w", err)
		logger.WarnContext(ctx, "compose down: list resource holders failed",
			"project", selection.Project, "workdir", selection.WorkDir, "error", err)
		// The CLI reports failed outcomes by count only, and no holder line
		// exists for this failure, so the event of the project carries its cause.
		s.report(cmp.Or(selection.Project, selection.WorkDir), PhaseError, err, nil)
		return []ReleaseOutcome{{Err: err}}
	}

	live := make(map[string]struct{}, len(replicas))
	for _, e := range replicas {
		live[e.Name] = struct{}{}
	}
	var stranded []resourceHolder
	for _, e := range entries {
		h, err := decodeHolder(e)
		if err != nil {
			logger.WarnContext(ctx, "compose down: skip unreadable resource holder",
				"holder", e.Name, "error", err)
			continue
		}
		if h.Release == nil {
			continue
		}
		if _, ok := live[h.Owner]; ok && !td.releaseLeft(h) {
			continue
		}
		stranded = append(stranded, h)
	}

	outcomes := make([]ReleaseOutcome, len(stranded))
	var eg errgroup.Group
	for i, h := range stranded {
		eg.Go(func() error {
			_, err := s.runRelease(ctx, h, td.force)
			if err != nil {
				logger.WarnContext(ctx, "compose down: release failed",
					"project", h.Ref.Project,
					"holder", h.name(),
					"error", err,
				)
			}
			outcomes[i] = ReleaseOutcome{Holder: h.name(), Err: err}
			return nil
		})
	}
	_ = eg.Wait()
	return outcomes
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
	td *teardown,
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
	return stopAllConcurrent(ctx, s, orphans, project, td)
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

// removeAllConcurrent removes all entries concurrently, each inside the remove
// hooks stored on it as td says, and returns outcomes. An entry td keeps is not
// removed; its outcome carries the stop hook failure that keeps it.
func removeAllConcurrent(
	ctx context.Context,
	s *Service,
	entries []cmdmanEntry,
	project string,
	td *teardown,
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
			outcome := RemoveOutcome{Command: name}
			if kept := td.keptBy(id); kept != nil {
				outcome.Err = fmt.Errorf(
					"remove command %q (%s): kept after a failed stop hook: %w", name, id, kept)
				s.report(name, PhaseSkipped, outcome.Err, nil)
			} else {
				s.report(name, PhaseRemoving, nil, nil)
				if err := s.teardownRemove(ctx, td, entry); err != nil {
					outcome.Err = fmt.Errorf("remove command %q (%s): %w", name, id, err)
					contextkey.ValueSlogLoggerDefault(ctx).WarnContext(ctx,
						"compose down: remove failed",
						"project", project,
						"command", name,
						"id", id,
						"error", err,
					)
					s.report(name, PhaseError, outcome.Err, nil)
				} else {
					s.report(name, PhaseRemoved, nil, nil)
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

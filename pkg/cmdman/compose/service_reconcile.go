package compose

import (
	"context"
	"fmt"
	"slices"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// reconcileWalkLimit caps the number of concurrent service actions during a
// reconcile walk. It is conservative; a CLI flag could later override it.
const reconcileWalkLimit = 8

// reconcileStart converges the targeted commands to a running state by walking
// the dependency graph down from begin. It is shared by compose up and
// spec-backed compose start.
//
// Dependencies are pulled into the closure transitively, so after.Condition is
// always evaluated against the run this reconciliation is responsible for, not
// against stale terminal state from a previous run.
func (s *Service) reconcileStart(
	ctx context.Context,
	spec ComposeSpec,
	names []string,
) ([]StartOutcome, error) {
	// Cycle validation stays strict (unlike Docker Compose, this project rejects
	// cyclic graphs) before any service action runs.
	if err := ValidateDAG(spec.Commands); err != nil {
		return nil, err
	}

	snaps, err := s.snapshotCommands(ctx, spec.WorkDir, spec.Project)
	if err != nil {
		return nil, err
	}

	closure := resolveTargetCommands(spec, names)
	g := buildReconcileGraph(spec, snaps, closure)

	limit := min(len(closure), reconcileWalkLimit)
	g.walk(ctx, walkFromBegin, limit, s.upStartAction)

	s.reportBlocked(g)
	return g.startOutcomes(spec), nil
}

// reportBlocked emits a terminal error event for every in-closure command the
// walk never acted on (blocked by an unsatisfied dependency, so the action — and
// thus its own terminal event — never ran). Without this, blocked commands would
// be silently absent from the state trace even though startOutcomes/stopOutcomes
// report them with an error. The walk has finished by the time this runs.
func (s *Service) reportBlocked(g *reconcileGraph) {
	if s.reporter == nil {
		return
	}
	type blocked struct {
		name string
		err  error
	}
	var pending []blocked
	g.mu.Lock()
	for _, v := range g.Vertices {
		if v.Command == nil || !v.InClosure || !v.Blocked {
			continue
		}
		pending = append(pending, blocked{name: v.Command.Name, err: v.Err})
	}
	g.mu.Unlock()
	for _, b := range pending {
		s.report(b.name, PhaseError, b.err, nil)
	}
}

// upStartAction starts (or confirms running) every replica of a command and,
// when a dependent needs its completion, waits for all replicas to stop so
// completion edges can progress from the current run's terminal state.
func (s *Service) upStartAction(
	ctx context.Context,
	g *reconcileGraph,
	v *graphVertex,
) actionResult {
	cmd := v.Command

	// Index the snapshot by replica name so each desired replica can be checked
	// for activeness before being (idempotently) started.
	active := make(map[string]bool, len(v.Snapshot.Instances))
	for _, in := range v.Snapshot.Instances {
		active[in.GenName] = in.State == model.EventTypeRunning ||
			in.State == model.EventTypeStarting
	}

	instances := cmd.InstanceNames()
	s.report(cmd.Name, PhaseStarting, nil, nil)
	for _, genName := range instances {
		// Idempotency: starting/running replicas are already active. Everything
		// else (created/exited/failed/absent) gets a Start, matching low-level
		// cmdman start and project-only compose start.
		if active[genName] {
			continue
		}
		if err := s.svc.Start(ctx, genName); err != nil {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: start failed",
				"command", cmd.Name,
				"generated_name", genName,
				"error", err,
			)
			werr := fmt.Errorf("start command %q (%s): %w", cmd.Name, genName, err)
			s.report(cmd.Name, PhaseError, werr, nil)
			return actionResult{State: v.Snapshot.State, Err: werr}
		}
	}

	// No dependent waits on our termination: record running and let dependents
	// on the running condition proceed without blocking on completion.
	if !g.anyDependentNeedsCompletion(v.ID) {
		s.report(cmd.Name, PhaseRunning, nil, v.Snapshot.ExitCode)
		return actionResult{State: model.EventTypeRunning, ExitCode: v.Snapshot.ExitCode}
	}

	// A completion edge depends on us: observe the terminal state of every
	// replica this run. cmdman.Service.Start returns nil even if a command exits
	// before running is observed, so completion must come from Wait(stopped),
	// never inferred from Start alone.
	s.report(cmd.Name, PhaseWaiting, nil, nil)
	results, werr := s.svc.Wait(ctx, cmdman.WaitRequest{
		Targets:   instances,
		Condition: cmdman.WaitConditionStopped,
	})
	if werr != nil {
		s.report(cmd.Name, PhaseError, werr, nil)
		return actionResult{State: model.EventTypeRunning, Err: werr}
	}

	// Reduce per-replica wait results to a single terminal state. A replica with
	// a recorded exit code exited (possibly non-zero); one without is a
	// monitor/subprocess failure. completed is satisfied by either; the
	// successful condition additionally checks the exit code (see aggregate).
	insts := make([]instanceSnapshot, 0, len(results))
	var firstErr error
	for _, r := range results {
		st := model.EventTypeExited
		if r.Err != nil {
			st = model.EventTypeFailed
			if firstErr == nil {
				firstErr = r.Err
			}
		} else if r.ExitCode == nil {
			st = model.EventTypeFailed
		}
		insts = append(insts, instanceSnapshot{State: st, ExitCode: r.ExitCode})
	}
	state, exit := aggregateInstances(insts)
	switch {
	case firstErr != nil:
		s.report(cmd.Name, PhaseFailed, firstErr, exit)
		return actionResult{State: model.EventTypeFailed, ExitCode: exit, Err: firstErr}
	case state == model.EventTypeExited:
		s.report(cmd.Name, PhaseExited, nil, exit)
		return actionResult{State: model.EventTypeExited, ExitCode: exit}
	default:
		s.report(cmd.Name, PhaseFailed, nil, exit)
		return actionResult{State: model.EventTypeFailed, ExitCode: exit}
	}
}

// reconcileStop converges the targeted commands to a stopped state by walking
// the dependency graph up from end: dependents are stopped before the
// dependencies they rely on. It is shared by spec-backed compose stop and the
// stop phase of compose down.
//
// Dependents are pulled into the closure transitively (resolveStopTargetCommands)
// so a dependency is never stopped while a command that depends on it is still
// running.
func (s *Service) reconcileStop(
	ctx context.Context,
	spec ComposeSpec,
	names []string,
) ([]StopOutcome, error) {
	if err := ValidateDAG(spec.Commands); err != nil {
		return nil, err
	}

	snaps, err := s.snapshotCommands(ctx, spec.WorkDir, spec.Project)
	if err != nil {
		return nil, err
	}

	closure := resolveStopTargetCommands(spec, names)
	g := buildReconcileGraph(spec, snaps, closure)

	limit := min(len(closure), reconcileWalkLimit)
	g.walk(
		ctx,
		walkFromEnd,
		limit,
		func(ctx context.Context, _ *reconcileGraph, v *graphVertex) actionResult {
			return s.stopAction(ctx, v, spec.Project)
		},
	)

	s.reportBlocked(g)
	return g.stopOutcomes(spec), nil
}

// stopAction stops a single command. Only a command with a live monitor
// (starting/running) is stopped; created/exited/failed are already terminal and
// a stop on them would only return monitor-connect errors, so they are no-ops.
func (s *Service) stopAction(
	ctx context.Context,
	v *graphVertex,
	project string,
) actionResult {
	cmd := v.Command
	snap := v.Snapshot

	live := snap.activeInstances()
	if len(live) == 0 {
		// Nothing to stop: every replica is already terminal. Report skipped, keep
		// the observed aggregate state for diagnostics.
		s.report(cmd.Name, PhaseSkipped, nil, snap.ExitCode)
		return actionResult{State: snap.State, ExitCode: snap.ExitCode}
	}

	ids := make([]string, len(live))
	for i, in := range live {
		ids[i] = in.ID
	}
	s.report(cmd.Name, PhaseStopping, nil, nil)
	if _, err := s.svc.Stop(ctx, cmdman.StopRequest{Targets: ids}); err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: stop failed",
			"project", project,
			"command", cmd.Name,
			"ids", ids,
			"error", err,
		)
		werr := fmt.Errorf("stop command %q (%v): %w", cmd.Name, ids, err)
		s.report(cmd.Name, PhaseError, werr, nil)
		return actionResult{State: snap.State, Err: werr}
	}
	s.report(cmd.Name, PhaseStopped, nil, snap.ExitCode)
	return actionResult{State: model.EventTypeExited, ExitCode: snap.ExitCode}
}

// snapshotCommands builds the pre-reconciliation command snapshot for a project.
// This is the service-level equivalent of `compose ps`: it uses the same
// project/workdir selection and exposes the same state, exit code, and command
// identity, so reconcile decisions and user-visible status agree.
func (s *Service) snapshotCommands(
	ctx context.Context,
	workDir, project string,
) (map[string]commandSnapshot, error) {
	existing, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(workDir, project),
	})
	if err != nil {
		return nil, fmt.Errorf("list existing commands before start: %w", err)
	}
	// Group replicas under their compose command name, then aggregate.
	byName := make(map[string][]instanceSnapshot, len(existing))
	for _, e := range existing {
		if e.ConfigJSON == nil {
			continue
		}
		name := e.ConfigJSON.Labels[LabelCommand]
		if name == "" {
			continue
		}
		byName[name] = append(byName[name], instanceSnapshot{
			ID:         e.ID,
			GenName:    e.Name,
			ScaleIndex: scaleIndexOf(e),
			State:      e.State,
			ExitCode:   e.ExitCode,
		})
	}
	out := make(map[string]commandSnapshot, len(byName))
	for name, insts := range byName {
		slices.SortFunc(insts, func(a, b instanceSnapshot) int {
			return a.ScaleIndex - b.ScaleIndex
		})
		state, exit := aggregateInstances(insts)
		out[name] = commandSnapshot{Instances: insts, State: state, ExitCode: exit}
	}
	return out, nil
}

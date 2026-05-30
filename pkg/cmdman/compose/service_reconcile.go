package compose

import (
	"context"
	"fmt"

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

	return g.startOutcomes(spec), nil
}

// upStartAction starts (or confirms running) a single command and, when a
// dependent needs its completion, waits for it to stop so completion edges can
// progress from the current run's terminal state.
func (s *Service) upStartAction(
	ctx context.Context,
	g *reconcileGraph,
	v *graphVertex,
) actionResult {
	cmd := v.Command
	state := v.Snapshot.State

	// Idempotency: starting/started are already active. created/exited/failed
	// (and any unexpected/absent state) get a Start, matching low-level cmdman
	// start and project-only compose start.
	active := state == model.EventTypeStarted || state == model.EventTypeStarting
	if !active {
		if err := s.svc.Start(ctx, cmd.GeneratedName); err != nil {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: start failed",
				"command", cmd.Name,
				"generated_name", cmd.GeneratedName,
				"error", err,
			)
			return actionResult{
				State: state,
				Err:   fmt.Errorf("start command %q (%s): %w", cmd.Name, cmd.GeneratedName, err),
			}
		}
	}

	// No dependent waits on our termination: record started and let dependents
	// on the started condition proceed without blocking on completion.
	if !g.anyDependentNeedsCompletion(v.ID) {
		return actionResult{State: model.EventTypeStarted, ExitCode: v.Snapshot.ExitCode}
	}

	// A completion edge depends on us: observe the terminal state of this run.
	// cmdman.Service.Start returns nil even if the command exits before started
	// is observed, so completion must come from Wait(stopped), never inferred
	// from Start alone.
	results, werr := s.svc.Wait(ctx, cmdman.WaitRequest{
		Targets:   []string{cmd.GeneratedName},
		Condition: cmdman.WaitConditionStopped,
	})
	if werr != nil {
		return actionResult{State: model.EventTypeStarted, Err: werr}
	}

	var exit *int
	if len(results) > 0 {
		exit = results[0].ExitCode
		if results[0].Err != nil {
			return actionResult{State: model.EventTypeFailed, ExitCode: exit, Err: results[0].Err}
		}
	}
	// A recorded exit code means a real exit (possibly non-zero); its absence
	// means a monitor/subprocess failure. completed is satisfied by either;
	// completed_successfully additionally checks the exit code.
	if exit != nil {
		return actionResult{State: model.EventTypeExited, ExitCode: exit}
	}
	return actionResult{State: model.EventTypeFailed}
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

	return g.stopOutcomes(spec), nil
}

// stopAction stops a single command. Only a command with a live monitor
// (starting/started) is stopped; created/exited/failed are already terminal and
// a stop on them would only return monitor-connect errors, so they are no-ops.
func (s *Service) stopAction(
	ctx context.Context,
	v *graphVertex,
	project string,
) actionResult {
	cmd := v.Command
	snap := v.Snapshot

	active := snap.State == model.EventTypeStarted || snap.State == model.EventTypeStarting
	if !active || snap.ID == "" {
		// Nothing to stop: keep the observed state for diagnostics.
		return actionResult{State: snap.State, ExitCode: snap.ExitCode}
	}

	if _, err := s.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{snap.ID}}); err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: stop failed",
			"project", project,
			"command", cmd.Name,
			"id", snap.ID,
			"error", err,
		)
		return actionResult{
			State: snap.State,
			Err:   fmt.Errorf("stop command %q (%s): %w", cmd.Name, snap.ID, err),
		}
	}
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
	out := make(map[string]commandSnapshot, len(existing))
	for _, e := range existing {
		if e.ConfigJSON == nil {
			continue
		}
		name := e.ConfigJSON.Labels[LabelCommand]
		if name == "" {
			continue
		}
		out[name] = commandSnapshot{
			ID:       e.ID,
			GenName:  e.Name,
			State:    e.State,
			ExitCode: e.ExitCode,
		}
	}
	return out, nil
}

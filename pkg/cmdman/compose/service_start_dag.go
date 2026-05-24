package compose

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

// depEvent is one of the events a command publishes about itself so dependents
// can evaluate their after.Condition. Each command publishes at most two events
// in order: a Started event (when Start returns successfully) and then a
// Stopped event (when Wait observes exited/failed). On failure, a single
// error-bearing event is published and the channel is closed.
type depEvent struct {
	Started  bool
	Stopped  bool
	ExitCode *int
	Err      error
}

// dagCommand is the per-command state used by startInDAGOrder.
type dagCommand struct {
	nc      Command
	genName string
	events  chan depEvent
}

// resolveTargetCommands resolves the supplied command-name subset to the set of
// commands to operate on, transitively pulling in every after-dependency so a
// targeted command can be created and started alongside what it needs.
// names == nil (or empty) → every command in the spec.
func resolveTargetCommands(spec ComposeSpec, names []string) map[string]struct{} {
	all := make(map[string]Command, len(spec.Commands))
	for _, nc := range spec.Commands {
		all[nc.Name] = nc
	}
	target := make(map[string]struct{})
	if len(names) == 0 {
		for n := range all {
			target[n] = struct{}{}
		}
		return target
	}
	var walk func(string)
	walk = func(n string) {
		if _, seen := target[n]; seen {
			return
		}
		nc, ok := all[n]
		if !ok {
			return
		}
		target[n] = struct{}{}
		for _, dep := range nc.After {
			walk(dep.Name)
		}
	}
	for _, n := range names {
		walk(n)
	}
	return target
}

// startInDAGOrder starts each desired command in a goroutine, respecting
// after.Condition by waiting on dep events before attempting Start. Returns
// per-command outcomes in spec order. Failures do not abort siblings
// (resolved-decision 21).
//
// When restrict is non-nil, only commands whose name is in restrict participate.
// Use commandsToStart to expand a user-supplied subset to include required
// dependencies.
func startInDAGOrder(
	ctx context.Context,
	svc cmdmanSvc,
	spec ComposeSpec,
	stateByCommand map[string]string,
	restrict map[string]struct{},
) []StartOutcome {
	// Pre-create per-command event channels for every command in the spec, so
	// dependents can always find their dep even when restrict excludes the dep
	// (in that case we publish a synthetic terminal event derived from the
	// pre-snapshot, if available).
	cmds := make(map[string]*dagCommand, len(spec.Commands))
	for _, nc := range spec.Commands {
		cmds[nc.Name] = &dagCommand{
			nc:      nc,
			genName: nc.GeneratedName,
			events:  make(chan depEvent, 2),
		}
	}

	var (
		mu       sync.Mutex
		outcomes = make(map[string]StartOutcome)
	)
	record := func(o StartOutcome) {
		mu.Lock()
		outcomes[o.Command] = o
		mu.Unlock()
	}

	// For commands excluded by restrict, synthesize terminal events so any
	// dependents inside restrict can still evaluate after-conditions against
	// the pre-snapshot state.
	for _, nc := range spec.Commands {
		if restrict != nil {
			if _, in := restrict[nc.Name]; in {
				continue
			}
		} else {
			continue
		}
		c := cmds[nc.Name]
		st := stateByCommand[nc.Name]
		switch st {
		case store.StateRunning, store.StateStarting:
			c.events <- depEvent{Started: true}
		case store.StateExited, store.StateFailed:
			c.events <- depEvent{Stopped: true}
		default:
			c.events <- depEvent{Err: fmt.Errorf(
				"dependency %q is not started and was excluded by command filter", nc.Name)}
		}
		close(c.events)
	}

	eg, gctx := errgroup.WithContext(ctx)

	for _, nc := range spec.Commands {
		if restrict != nil {
			if _, ok := restrict[nc.Name]; !ok {
				continue
			}
		}
		c := cmds[nc.Name]

		eg.Go(func() error {
			defer close(c.events)

			// 1. Wait for every dependency's condition.
			for _, dep := range c.nc.After {
				if err := waitForCondition(gctx, svc, cmds, stateByCommand,
					dep, c.nc.Name); err != nil {
					record(StartOutcome{Command: c.nc.Name, Err: err})
					c.events <- depEvent{Err: err}
					return nil
				}
			}

			// 2. Idempotency: skip Start if already running or starting.
			if st := stateByCommand[c.nc.Name]; st == store.StateRunning ||
				st == store.StateStarting {
				record(StartOutcome{Command: c.nc.Name})
				c.events <- depEvent{Started: true}
				return nil
			}

			// 3. Start.
			if err := svc.Start(gctx, c.genName); err != nil {
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: start failed",
					"command", c.nc.Name,
					"generated_name", c.genName,
					"error", err,
				)
				wrapped := fmt.Errorf("start command %q (%s): %w",
					c.nc.Name, c.genName, err)
				record(StartOutcome{Command: c.nc.Name, Err: wrapped})
				c.events <- depEvent{Err: wrapped}
				return nil
			}
			record(StartOutcome{Command: c.nc.Name})
			// Publish Started so dependents on the "started" condition can proceed.
			c.events <- depEvent{Started: true}

			// 4. Wait for completion so dependents that need
			// completed/completed_successfully can proceed. Skip Wait if no
			// command depends on our termination — keeps the goroutine pool
			// thin for projects with mostly "started" deps.
			if !anyAwaitsCompletion(spec, c.nc.Name) {
				return nil
			}

			results, werr := svc.Wait(gctx, cmdman.WaitRequest{
				Targets:   []string{c.genName},
				Condition: cmdman.WaitConditionStopped,
			})
			ev := depEvent{Started: true, Stopped: true}
			if werr != nil {
				ev.Err = werr
			} else if len(results) > 0 {
				ev.ExitCode = results[0].ExitCode
				if results[0].Err != nil {
					ev.Err = results[0].Err
				}
			}
			c.events <- ev
			return nil
		})
	}

	_ = eg.Wait()

	// Drain outcomes in deterministic spec order.
	var ordered []StartOutcome
	mu.Lock()
	for _, nc := range spec.Commands {
		if restrict != nil {
			if _, ok := restrict[nc.Name]; !ok {
				continue
			}
		}
		if o, ok := outcomes[nc.Name]; ok {
			ordered = append(ordered, o)
		} else {
			ordered = append(ordered, StartOutcome{
				Command: nc.Name,
				Err:     errors.New("internal: missing outcome"),
			})
		}
	}
	mu.Unlock()
	return ordered
}

// anyAwaitsCompletion reports whether any command in spec depends on cmdName
// with a condition that requires its termination.
func anyAwaitsCompletion(spec ComposeSpec, cmdName string) bool {
	for _, nc := range spec.Commands {
		for _, dep := range nc.After {
			if dep.Name != cmdName {
				continue
			}
			if dep.Condition == ConditionCompleted ||
				dep.Condition == ConditionCompletedSuccessfully {
				return true
			}
		}
	}
	return false
}

// waitForCondition blocks until the dependency identified by dep satisfies its
// declared condition, or returns an error explaining why it can no longer be
// satisfied.
func waitForCondition(
	ctx context.Context,
	svc cmdmanSvc,
	cmds map[string]*dagCommand,
	stateByCommand map[string]string,
	dep AfterSpec,
	dependentName string,
) error {
	depCmd, ok := cmds[dep.Name]
	if !ok {
		return fmt.Errorf("dependency %q for %q not in spec", dep.Name, dependentName)
	}

	// Fast path: dep is already in a terminal state per the pre-snapshot.
	// PLAN line 506-510 says "if a dependency was already stopped before the
	// operation begins, completed and completed_successfully may pass based on
	// current stored state and exit code."
	if preState, snap := stateByCommand[dep.Name]; snap {
		switch dep.Condition {
		case ConditionStarted:
			if preState == store.StateRunning ||
				preState == store.StateStarting {
				return nil
			}
		case ConditionCompleted:
			if preState == store.StateExited || preState == store.StateFailed {
				return nil
			}
		case ConditionCompletedSuccessfully:
			if preState == store.StateExited || preState == store.StateFailed {
				return checkExitZero(ctx, svc, depCmd.genName, dep.Name)
			}
		}
	}

	// Otherwise, read events from dep's channel.
	for ev := range depCmd.events {
		if ev.Err != nil {
			return fmt.Errorf("dependency %q failed: %w", dep.Name, ev.Err)
		}
		switch dep.Condition {
		case ConditionStarted:
			if ev.Started {
				return nil
			}
		case ConditionCompleted:
			if ev.Stopped {
				return nil
			}
		case ConditionCompletedSuccessfully:
			if ev.Stopped {
				if ev.ExitCode != nil && *ev.ExitCode == 0 {
					return nil
				}
				code := "<absent>"
				if ev.ExitCode != nil {
					code = fmt.Sprintf("%d", *ev.ExitCode)
				}
				return fmt.Errorf(
					"dependency %q condition completed_successfully not satisfied (exit code %s)",
					dep.Name, code,
				)
			}
		}
	}
	return fmt.Errorf("dependency %q condition %q not satisfied (no terminal event)",
		dep.Name, dep.Condition)
}

// checkExitZero re-evaluates an already-stopped dependency's exit code from
// the store for the completed_successfully fast path.
func checkExitZero(ctx context.Context, svc cmdmanSvc, genName, depName string) error {
	results, err := svc.Wait(ctx, cmdman.WaitRequest{
		Targets:   []string{genName},
		Condition: cmdman.WaitConditionStopped,
	})
	if err != nil {
		return fmt.Errorf("check exit code for %q: %w", depName, err)
	}
	if len(results) == 0 || results[0].ExitCode == nil {
		return fmt.Errorf(
			"dependency %q condition completed_successfully not satisfied (exit code absent)",
			depName,
		)
	}
	if *results[0].ExitCode != 0 {
		return fmt.Errorf(
			"dependency %q condition completed_successfully not satisfied (exit code %d)",
			depName, *results[0].ExitCode,
		)
	}
	return nil
}

package compose

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

const (
	// hookOutputDrain bounds how long the output of a hook is still forwarded
	// once its exec command stopped. The stream normally ends with the run; the
	// bound keeps a monitor that never closes it from holding the hook open.
	hookOutputDrain = 2 * time.Second
	// hookStopTimeout bounds stopping the exec command of a cancelled hook.
	// cmdman waits up to 10s for the stop signal to work and as long again
	// after SIGKILL.
	hookStopTimeout = 30 * time.Second
)

// runLifecycleEvent runs ev of every hook in hooks that sets it, one after
// another in declaration order, for replica r.
//
// A hook whose on_error is fail stops the walk: the error is returned and is
// the replica's failure. One whose on_error is continue is reported as a
// warning event and returned among warnings. One whose on_error is ignore is
// reported as an ignored event and adds no warning. A cancelled ctx stops the
// walk whatever on_error says.
func (s *Service) runLifecycleEvent(
	ctx context.Context,
	r hookReplica,
	hooks []LifecycleHook,
	ev LifecycleEvent,
) (warnings []error, err error) {
	for _, h := range hooks {
		exec, ok := h.Events[ev]
		if !ok {
			continue
		}
		warning, err := s.runHook(ctx, r, h, ev, exec)
		if err != nil {
			return warnings, err
		}
		if warning != nil {
			warnings = append(warnings, warning)
		}
	}
	return warnings, nil
}

// hookRun names one run of one hook event in its progress events.
type hookRun struct {
	replica hookReplica
	hook    string
	event   LifecycleEvent
	exec    string
	// env is the environment of the exec command, less the resource value.
	env []string
}

func (s *Service) reportHook(run hookRun, phase Phase, err error, exit *int) {
	if s.reporter == nil {
		return
	}
	s.reporter.Report(Event{
		Command:    run.replica.Display,
		Phase:      phase,
		Err:        err,
		ExitCode:   exit,
		ScaleIndex: run.replica.ScaleIndex,
		Hook:       run.hook,
		Lifecycle:  run.event,
		Exec:       run.exec,
	})
}

func (s *Service) reportHookOutput(run hookRun, stream logdriver.Stream, line string) {
	if s.reporter == nil {
		return
	}
	s.reporter.Report(Event{
		Command:    run.replica.Display,
		Phase:      PhaseHookOutput,
		ScaleIndex: run.replica.ScaleIndex,
		Hook:       run.hook,
		Lifecycle:  run.event,
		Exec:       run.exec,
		Stream:     stream,
		Line:       line,
	})
}

// runHook runs ev of h for r and applies h's on_error to a failure. It returns
// the failure as err for on_error fail, as warning for on_error continue, and
// neither for on_error ignore.
func (s *Service) runHook(
	ctx context.Context,
	r hookReplica,
	h LifecycleHook,
	ev LifecycleEvent,
	exec LifecycleExec,
) (warning, err error) {
	return s.runHookAs(ctx, hookRun{
		replica: r,
		hook:    h.Name,
		event:   ev,
		exec:    ExecCommandName(r.Name, h.Name, ev),
		env:     slices.Concat(r.Env, s.hookEnv(r, h, ev)),
	}, h, exec)
}

// runRelease runs the release stored in holder h, whose replica is gone, as
// the release hook would have run: in the directory and environment h keeps
// for it, with the resource key and value added. A failure is handled as
// [Service.runHook] handles it under the on_error h stores, except that force
// turns on_error fail into continue.
func (s *Service) runRelease(
	ctx context.Context,
	h resourceHolder,
	force bool,
) (warning, err error) {
	rel := h.Release
	if rel == nil {
		return nil, fmt.Errorf("resource holder %s stores no release", h.name())
	}
	hooks := []LifecycleHook{{
		Name:     h.hookName(),
		Resource: h.Ref.Key,
		Events: map[LifecycleEvent]LifecycleExec{
			rel.Event: {Args: rel.Args, OnError: rel.OnError},
		},
	}}
	if force {
		hooks = forcedHooks(hooks)
	}
	hook := hooks[0]
	r := h.replica()
	return s.runHookAs(ctx, hookRun{
		replica: r,
		hook:    hook.Name,
		event:   rel.Event,
		exec:    ExecCommandName(r.Name, hook.Name, rel.Event),
		env:     append(slices.Clone(h.Env), ENV_CMDMAN_COMPOSE_RESOURCE_KEY+"="+h.Ref.Key),
	}, hook, hook.Events[rel.Event])
}

// runHookAs is [Service.runHook] for the run described by run.
//
// The failure of a release event goes to the release recorder ctx carries,
// whatever on_error says, along with the holder as it was before the run.
func (s *Service) runHookAs(
	ctx context.Context,
	run hookRun,
	h LifecycleHook,
	exec LifecycleExec,
) (warning, err error) {
	r := run.replica
	ev := run.event
	exit, held, err := s.execHook(ctx, run, h, exec)
	if err == nil {
		s.reportHook(run, PhaseHookSucceeded, nil, exit)
		return nil, nil
	}
	err = fmt.Errorf("hook %q %s of %s: %w", h.Name, ev, r.Display, err)
	if h.Resource != "" && !ev.acquires() {
		onError := exec.OnError.resolved()
		if ctx.Err() != nil {
			onError = OnErrorFail
		}
		if held == nil {
			held = &resourceHolder{
				Ref:   r.resourceRef(h.Resource),
				Owner: r.Name,
				Release: &resourceRelease{
					Event:   ev,
					Args:    slices.Clone(exec.Args),
					OnError: exec.OnError.resolved(),
				},
				Dir: r.Dir,
				Env: slices.Clone(run.env),
			}
		}
		releaseRecorderFrom(ctx).record(failedRelease{
			holder:  *held,
			display: r.Display,
			err:     err,
			onError: onError,
		})
	}
	if ctx.Err() != nil {
		s.reportHook(run, PhaseHookFailed, err, exit)
		return nil, err
	}
	switch exec.OnError.resolved() {
	case OnErrorContinue:
		s.reportHook(run, PhaseHookWarning, err, exit)
		return err, nil
	case OnErrorIgnore:
		// The run still needs a terminal event, or a progress view keeps it in
		// flight forever.
		s.reportHook(run, PhaseHookIgnored, err, exit)
		logger := contextkey.ValueSlogLoggerDefault(ctx)
		logger.InfoContext(ctx, "compose: ignoring failed hook", "error", err)
		if h.Resource != "" && !ev.acquires() {
			// Releasing the resource is what the failed hook was for, and
			// ignore says its failure does not matter, so the value is
			// dropped as if the release had worked.
			if err := s.dropHolder(ctx, r.resourceRef(h.Resource)); err != nil {
				logger.WarnContext(ctx, "compose: drop resource after ignored release failure",
					"resource", h.Resource, "error", err)
			}
		}
		return nil, nil
	default:
		s.reportHook(run, PhaseHookFailed, err, exit)
		return nil, err
	}
}

// execHook runs ev of h as the exec command run.exec and records what it
// acquired or released. The exec command is removed when everything worked and
// kept otherwise, so a failure can be read back with cmdman logs and inspect.
// The failure of a release names the value it was to release, which the
// holder keeps unless on_error is ignore. held is the holder of the resource h
// declares as it was before the run, or nil when there is none or it cannot be
// read.
func (s *Service) execHook(
	ctx context.Context,
	run hookRun,
	h LifecycleHook,
	exec LifecycleExec,
) (exit *int, held *resourceHolder, err error) {
	r := run.replica
	var value string
	if h.Resource != "" {
		holder, err := s.findHolder(ctx, r.resourceRef(h.Resource))
		if err != nil {
			return nil, nil, err
		}
		if holder != nil && holder.ConfigJSON != nil {
			value = holder.ConfigJSON.Labels[LabelResourceValue]
			if decoded, err := decodeHolder(*holder); err == nil {
				held = &decoded
			}
		}
	}
	exit, err = s.execHookValue(ctx, run, h, exec, value)
	if err != nil && h.Resource != "" && !run.event.acquires() {
		err = fmt.Errorf("release resource %q (value %q): %w", h.Resource, value, err)
	}
	return exit, held, err
}

// execHookValue is [Service.execHook] once value, the stored value of the
// resource h declares, has been read.
func (s *Service) execHookValue(
	ctx context.Context,
	run hookRun,
	h LifecycleHook,
	exec LifecycleExec,
	value string,
) (*int, error) {
	r := run.replica
	labels := execLabels(r, h.Name, run.event)
	if err := s.clearStaleExecs(ctx, labels); err != nil {
		return nil, err
	}

	env := slices.Clone(run.env)
	if h.Resource != "" {
		env = append(env, ENV_CMDMAN_COMPOSE_RESOURCE_VALUE+"="+value)
	}
	created, err := s.svc.Create(ctx, cmdman.CreateRequest{
		Name:          run.exec,
		Dir:           r.Dir,
		Argv:          exec.Args,
		Env:           env,
		ImportHostEnv: new(false),
		// The exec command's stdout is read back for a resource value, which
		// needs a driver that keeps the output.
		LogDriver:     logdriver.DriverK8sFile,
		RestartPolicy: model.RestartPolicyNo,
		Labels:        labels,
	})
	if err != nil {
		return nil, fmt.Errorf("create hook command %s: %w", run.exec, err)
	}

	s.reportHook(run, PhaseHookRunning, nil, nil)
	exit, err := s.runExec(ctx, run, created.ID)
	if err != nil {
		return exit, err
	}

	if h.Resource != "" {
		if err := s.recordResource(ctx, run, h, created.ID); err != nil {
			return exit, err
		}
	}

	if err := s.removeIntermediate(ctx, created.ID); err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(ctx,
			"compose: remove finished hook command", "command", run.exec, "error", err)
	}
	return exit, nil
}

// hookEnv returns what an exec command of r gets on top of r's environment to
// run ev of h. The resource value is left out: it changes from run to run,
// while the rest is also what a holder keeps for the release hook.
func (s *Service) hookEnv(r hookReplica, h LifecycleHook, ev LifecycleEvent) []string {
	env := composeContextEnv(r.Project, r.WorkDir, r.Command, r.ScaleIndex, r.Scale)
	env = append(env,
		ENV_CMDMAN_COMPOSE_HOOK_NAME+"="+h.Name,
		ENV_CMDMAN_COMPOSE_HOOK_EVENT+"="+string(ev),
	)
	if h.Resource != "" {
		env = append(env, ENV_CMDMAN_COMPOSE_RESOURCE_KEY+"="+h.Resource)
	}
	// A cmdman run by the hook, such as `cmdman compose resource get`, has to
	// reach the store this service uses.
	cfg := s.svc.Config()
	env = append(env,
		cmdman.ENV_CMDMAN_DATA_DIR+"="+cfg.DataDir,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+cfg.RuntimeDir,
	)
	if cfg.ConfigPath != "" {
		// The hook runs in the replica's directory, where a relative --config
		// value would name some other file.
		path := cfg.ConfigPath
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		env = append(env, cmdman.ENV_CMDMAN_CONF+"="+path)
	}
	return env
}

// runExec starts the exec command id, forwards its output as hook-output
// events while it runs, and waits for it to stop. It fails unless the command
// exited with code 0. When ctx is cancelled it stops the command before it
// returns, so no hook outlives the operation that ran it.
func (s *Service) runExec(ctx context.Context, run hookRun, id string) (*int, error) {
	if err := s.svc.Start(ctx, id); err != nil {
		if ctx.Err() != nil {
			s.stopCancelledExec(ctx, run, id)
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("start hook command %s: %w", run.exec, err)
	}

	followCtx, cancelFollow := context.WithCancel(ctx)
	defer cancelFollow()
	var g errgroup.Group
	if s.reporter != nil {
		g.Go(func() error {
			s.forwardHookOutput(followCtx, run, id)
			return nil
		})
	}
	results, waitErr := s.svc.Wait(ctx, cmdman.WaitRequest{Targets: []string{id}})
	drain := time.AfterFunc(hookOutputDrain, cancelFollow)
	_ = g.Wait()
	drain.Stop()

	if ctx.Err() != nil {
		s.stopCancelledExec(ctx, run, id)
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, fmt.Errorf("wait for hook command %s: %w", run.exec, waitErr)
	}
	return hookExit(run.exec, results)
}

// hookExit judges the outcome of an exec command from its wait results. Only
// an exit code of 0 is a success: a wait for "stopped" also ends on a monitor
// that failed without any exit code.
func hookExit(name string, results []cmdman.WaitResult) (*int, error) {
	if len(results) != 1 {
		return nil, fmt.Errorf("wait for hook command %s: got %d results", name, len(results))
	}
	res := results[0]
	switch {
	case res.Err != nil:
		return res.ExitCode, fmt.Errorf("wait for hook command %s: %w", name, res.Err)
	case res.ExitCode == nil:
		return nil, fmt.Errorf("hook command %s ended without an exit code", name)
	case *res.ExitCode != 0:
		return res.ExitCode, fmt.Errorf("hook command %s exited with code %d", name, *res.ExitCode)
	default:
		return res.ExitCode, nil
	}
}

// stopCancelledExec stops the exec command id of a cancelled hook when it is
// starting or running. A start cut short can leave it never started. ctx is
// already done, so the stop runs on a context of its own that keeps ctx's
// values.
func (s *Service) stopCancelledExec(ctx context.Context, run hookRun, id string) {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hookStopTimeout)
	defer cancel()
	logger := contextkey.ValueSlogLoggerDefault(ctx)
	entries, err := s.svc.List(stopCtx, cmdman.ListRequest{
		AllStates: true,
		Labels:    execLabels(run.replica, run.hook, run.event),
	})
	if err != nil {
		logger.WarnContext(ctx,
			"compose: look up cancelled hook command", "command", run.exec, "error", err)
		return
	}
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		if err := s.stopLiveIntermediate(stopCtx, e); err != nil {
			logger.WarnContext(ctx,
				"compose: stop cancelled hook command", "command", run.exec, "error", err)
		}
	}
}

// recordResource stores what the exec command id of an acquire event printed
// as the resource value of h, or drops the value once a release event worked.
func (s *Service) recordResource(
	ctx context.Context,
	run hookRun,
	h LifecycleHook,
	id string,
) error {
	r := run.replica
	ref := r.resourceRef(h.Resource)
	if !run.event.acquires() {
		return s.dropHolder(ctx, ref)
	}
	value, err := s.readResourceValue(ctx, id)
	if err != nil {
		return fmt.Errorf("read resource %q from %s: %w", h.Resource, run.exec, err)
	}
	release := hookRelease(h, run.event)
	envEvent := run.event
	if release != nil {
		envEvent = release.Event
	}
	return s.putHolder(ctx, resourceHolder{
		Ref:     ref,
		Owner:   r.Name,
		Value:   value,
		Release: release,
		Dir:     r.Dir,
		Env:     slices.Concat(r.Env, s.hookEnv(r, h, envEvent)),
	})
}

// hookRelease returns the event of h that releases the resource acquired at
// acquire, or nil when h sets none.
func hookRelease(h LifecycleHook, acquire LifecycleEvent) *resourceRelease {
	for _, ev := range acquire.releases() {
		if exec, ok := h.Events[ev]; ok {
			return &resourceRelease{
				Event:   ev,
				Args:    slices.Clone(exec.Args),
				OnError: exec.OnError.resolved(),
			}
		}
	}
	return nil
}

package compose

import (
	"context"
	"sync"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// The *WithHooks helpers wrap one lifecycle operation of one replica in the
// pre and post events of its hooks. A hook that fails under on_error fail ends
// the helper with the hook's error and leaves the rest undone: a failing pre
// event skips the operation, and a failing post event comes after an operation
// that already happened. Warnings of on_error continue reach the reporter
// through the runner's events and change nothing here.

// hookSource tells where the hooks of an existing replica come from.
type hookSource int

const (
	// hooksFromSpec takes them from the command of a spec loaded from a compose
	// file.
	hooksFromSpec hookSource = iota
	// hooksFromStored decodes them from the replica's own stored label. A spec
	// rebuilt from stored labels carries no hooks, nor the directory and
	// environment a hook runs with.
	hooksFromStored
)

// replicaHooks returns the replica e of cmd as its hooks see it, and the hooks
// src says to run for it.
func (s *Service) replicaHooks(
	src hookSource,
	spec ComposeSpec,
	cmd Command,
	e cmdmanEntry,
) (hookReplica, []LifecycleHook, error) {
	if src == hooksFromStored {
		return storedHookReplica(e)
	}
	return s.specHookReplica(spec, cmd, scaleIndexOf(e)), cmd.Hooks, nil
}

// createWithHooks runs create_pre of hooks for r, creates the replica req
// describes, then runs create_post.
func (s *Service) createWithHooks(
	ctx context.Context,
	r hookReplica,
	hooks []LifecycleHook,
	req cmdman.CreateRequest,
) error {
	if _, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleCreatePre); err != nil {
		return err
	}
	if _, err := s.svc.Create(ctx, req); err != nil {
		return err
	}
	_, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleCreatePost)
	return err
}

// startWithHooks runs start_pre of hooks for r, starts the replica idOrName,
// then runs start_post.
func (s *Service) startWithHooks(
	ctx context.Context,
	r hookReplica,
	hooks []LifecycleHook,
	idOrName string,
) error {
	if _, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleStartPre); err != nil {
		return err
	}
	if err := s.svc.Start(ctx, idOrName); err != nil {
		return err
	}
	_, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleStartPost)
	return err
}

// startReplica starts the stored replica e of cmd inside the start hooks src
// says to run for it.
func (s *Service) startReplica(
	ctx context.Context,
	spec ComposeSpec,
	src hookSource,
	cmd Command,
	e cmdmanEntry,
) error {
	r, hooks, err := s.replicaHooks(src, spec, cmd, e)
	if err != nil {
		return err
	}
	return s.startWithHooks(ctx, r, hooks, e.Name)
}

// stopWithHooks runs stop_pre of hooks for r, stops the replica id and waits
// for it to terminate, then runs stop_post. hookFailed reports that err is the
// failure of a hook rather than of the stop: the replica still runs after a
// failed stop_pre and has stopped after a failed stop_post.
func (s *Service) stopWithHooks(
	ctx context.Context,
	r hookReplica,
	hooks []LifecycleHook,
	id string,
) (hookFailed bool, err error) {
	if _, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleStopPre); err != nil {
		return true, err
	}
	if err := s.stopForRecreate(ctx, id); err != nil {
		return false, err
	}
	if _, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleStopPost); err != nil {
		return true, err
	}
	return false, nil
}

// removeWithHooks runs remove_pre of hooks for r, removes the replica req
// targets, then runs remove_post. remove_post still finds the resource values
// of r: a holder outlives its replica. removed reports whether the replica is
// gone, which a failed remove_post does not change.
func (s *Service) removeWithHooks(
	ctx context.Context,
	r hookReplica,
	hooks []LifecycleHook,
	req cmdman.RemoveRequest,
) (removed bool, err error) {
	if _, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleRemovePre); err != nil {
		return false, err
	}
	results, err := s.svc.Remove(ctx, req)
	if err != nil {
		return false, err
	}
	for _, res := range results {
		if res.Err != nil {
			return false, res.Err
		}
	}
	_, err = s.runLifecycleEvent(ctx, r, hooks, LifecycleRemovePost)
	return true, err
}

// forcedHooks returns hooks with every on_error fail, explicit or by default,
// turned into continue, so a failing hook is warned about and the operation
// goes on. ignore is left as it is: it already lets the operation go on, and a
// release that fails under it still drops the resource.
func forcedHooks(hooks []LifecycleHook) []LifecycleHook {
	out := make([]LifecycleHook, len(hooks))
	for i, h := range hooks {
		events := make(map[LifecycleEvent]LifecycleExec, len(h.Events))
		for ev, exec := range h.Events {
			if exec.OnError.resolved() == OnErrorFail {
				exec.OnError = OnErrorContinue
			}
			events[ev] = exec
		}
		h.Events = events
		out[i] = h
	}
	return out
}

// stopReplica stops the live replica e inside the stop hooks stored on it. With
// force every hook failure passes as on_error continue. hookFailed reports as
// [Service.stopWithHooks] does, and also for hooks that cannot be read, which
// leaves e running.
func (s *Service) stopReplica(
	ctx context.Context,
	e cmdmanEntry,
	force bool,
) (hookFailed bool, err error) {
	r, hooks, err := storedHookReplica(e)
	if err != nil {
		return true, err
	}
	if force {
		hooks = forcedHooks(hooks)
	}
	return s.stopWithHooks(ctx, r, hooks, e.ID)
}

// teardown is what the replicas one compose stop or down tears down share. It
// is safe for concurrent use.
type teardown struct {
	// force lets every hook failure pass as on_error continue.
	force bool

	mu sync.Mutex
	// kept maps the ID of every replica a failed stop hook keeps from being
	// removed to that failure.
	kept map[string]error
}

// keptBy returns the stop hook failure that keeps the replica id, or nil.
func (t *teardown) keptBy(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kept[id]
}

// teardownStop stops the live replica e for t inside the stop hooks stored on
// it. A failed stop hook keeps e from the removal that follows in a down; a
// failed stop alone does not, as down removes such a replica by force.
func (s *Service) teardownStop(ctx context.Context, t *teardown, e cmdmanEntry) error {
	hookFailed, err := s.stopReplica(ctx, e, t.force)
	if hookFailed {
		t.mu.Lock()
		if t.kept == nil {
			t.kept = make(map[string]error)
		}
		t.kept[e.ID] = err
		t.mu.Unlock()
	}
	return err
}

// teardownRemove removes the replica e for t inside the remove hooks stored on
// it, by force should it still run. Once e is gone, the exec commands its
// failed hooks left for inspection go with it: no later operation of e would
// replace or remove them.
func (s *Service) teardownRemove(ctx context.Context, t *teardown, e cmdmanEntry) error {
	r, hooks, err := storedHookReplica(e)
	if err != nil {
		return err
	}
	if t.force {
		hooks = forcedHooks(hooks)
	}
	removed, err := s.removeWithHooks(ctx, r, hooks, cmdman.RemoveRequest{
		Targets: []string{e.ID},
		Force:   true,
	})
	if removed {
		s.removeLeftExecs(ctx, r)
	}
	return err
}

// removeLeftExecs removes the exec commands of r that are not running. A
// failure is logged, not returned: it leaves a record for inspection behind
// and touches nothing of r.
func (s *Service) removeLeftExecs(ctx context.Context, r hookReplica) {
	logger := contextkey.ValueSlogLoggerDefault(ctx)
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels: map[string]string{
			LabelIntermediate: IntermediateExec,
			LabelOwner:        r.Name,
			LabelHooksProject: r.Project,
			LabelHooksWorkdir: r.WorkDir,
		},
	})
	if err != nil {
		logger.WarnContext(ctx, "compose: look up hook commands left by failed hooks",
			"command", r.Name, "error", err)
		return
	}
	for _, e := range entries {
		if e.State == model.EventTypeRunning || e.State == model.EventTypeStarting {
			continue
		}
		if err := s.removeIntermediate(ctx, e.ID); err != nil {
			logger.WarnContext(ctx, "compose: remove hook command left by a failed hook",
				"command", e.Name, "error", err)
		}
	}
}

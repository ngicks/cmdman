package compose

import (
	"context"
	"errors"
	"fmt"
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
// of r: a holder outlives its replica. A failed remove_post leaves the replica
// removed, which removed reports. Once the replica is gone, the exec commands
// its failed hooks left for inspection go with it, since no later operation of
// the replica would replace or remove them. These include the one a failed
// remove_post just left.
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
	s.removeLeftExecs(ctx, r)
	return true, err
}

// removeReplica removes the replica r inside hooks as [Service.removeWithHooks]
// does. stopped tells whether the operation stopped r first. One it did not
// stop, as it stops only a starting or running replica, never ran the stop_pre
// or stop_post release of a resource a start of r acquired. Once such an r is
// removed, removeReplica runs those releases as compose down does
// ([teardown.releaseLeft]), and their failures join err. An r left in place
// keeps its resources.
func (s *Service) removeReplica(
	ctx context.Context,
	r hookReplica,
	hooks []LifecycleHook,
	req cmdman.RemoveRequest,
	stopped bool,
) error {
	removed, err := s.removeWithHooks(ctx, r, hooks, req)
	if removed && !stopped {
		err = errors.Join(err, s.releaseStopResources(ctx, r))
	}
	return err
}

// releaseStopResources runs the stored stop_pre or stop_post release of every
// resource held for the replica r, as [Service.runRelease] runs it under the
// on_error stored with it. Every release is tried, and their failures are
// returned. A release that fails keeps its value for the next compose down. A
// holder that cannot be read is left alone with a warning, as compose down
// leaves it.
func (s *Service) releaseStopResources(ctx context.Context, r hookReplica) error {
	logger := contextkey.ValueSlogLoggerDefault(ctx)
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels: map[string]string{
			LabelIntermediate: IntermediateHolder,
			LabelOwner:        r.Name,
			LabelHooksProject: r.Project,
			LabelHooksWorkdir: r.WorkDir,
		},
	})
	if err != nil {
		return fmt.Errorf("look up resource holders of %s: %w", r.Name, err)
	}
	var errs []error
	for _, e := range entries {
		h, err := decodeHolder(e)
		if err != nil {
			logger.WarnContext(ctx, "compose: skip unreadable resource holder",
				"holder", e.Name, "error", err)
			continue
		}
		if !h.releasedAtStop() {
			continue
		}
		if _, err := s.runRelease(ctx, h, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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

// teardownHooks returns the stored replica e as its hooks see it, and the hooks
// stored on it to run around step, "stop" or "remove". With force every hook
// failure passes as on_error continue, and hooks that cannot be decoded are
// left out with a warning: e is torn down without them. Without force such
// hooks fail step and leave e as it is.
func (s *Service) teardownHooks(
	ctx context.Context,
	e cmdmanEntry,
	force bool,
	step string,
) (hookReplica, []LifecycleHook, error) {
	r, err := storedReplica(e)
	if err != nil {
		return hookReplica{}, nil, err
	}
	hooks, err := storedHooks(e)
	switch {
	case err != nil && !force:
		return hookReplica{}, nil, err
	case err != nil:
		warning := fmt.Errorf("%s without the stored hooks: %w", step, err)
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(ctx,
			"compose: tear down without the stored hooks",
			"command", e.Name, "id", e.ID, "step", step, "error", err)
		// No hook ran, so the event names no hook and lands on the replica's own
		// line.
		s.report(r.Display, PhaseHookWarning, warning, nil)
		return r, nil, nil
	case force:
		return r, forcedHooks(hooks), nil
	default:
		return r, hooks, nil
	}
}

// stopReplica stops the live replica e inside the stop hooks stored on it, as
// [Service.teardownHooks] says for force. hookFailed reports as
// [Service.stopWithHooks] does. It also reports stored hooks that
// [Service.teardownHooks] refuses; e keeps running then.
func (s *Service) stopReplica(
	ctx context.Context,
	e cmdmanEntry,
	force bool,
) (hookFailed bool, err error) {
	r, hooks, err := s.teardownHooks(ctx, e, force, "stop")
	if err != nil {
		return true, err
	}
	return s.stopWithHooks(ctx, r, hooks, e.ID)
}

// teardown is what the replicas one compose stop or down tears down share. It
// is safe for concurrent use.
type teardown struct {
	// force lets every hook failure pass as on_error continue, and tears a
	// replica whose stored hooks cannot be decoded down without them.
	force bool

	mu sync.Mutex
	// kept maps the ID of every replica a failed stop hook keeps from being
	// removed to that failure.
	kept map[string]error
	// stopped holds the name of every replica t set out to stop.
	stopped map[string]bool
	// removed holds the name of every replica t removed.
	removed map[string]bool
}

// keptBy returns the stop hook failure that keeps the replica id, or nil.
func (t *teardown) keptBy(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kept[id]
}

func (t *teardown) markStopped(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped == nil {
		t.stopped = make(map[string]bool)
	}
	t.stopped[name] = true
}

func (t *teardown) markRemoved(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.removed == nil {
		t.removed = make(map[string]bool)
	}
	t.removed[name] = true
}

// releaseLeft reports whether t removed the replica h is held for without
// running the release of h: the release is a stop event, and t removed the
// replica without stopping it, as happens to a replica that is not starting or
// running. Every other release of a removed replica ran with the stop or
// remove hooks of t, or was skipped along with them when a step before it
// failed or the hooks could not be read. Such a release waits for the next
// down.
func (t *teardown) releaseLeft(h resourceHolder) bool {
	if !h.releasedAtStop() {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.removed[h.Owner] && !t.stopped[h.Owner]
}

// teardownStop stops the live replica e for t inside the stop hooks stored on
// it. A failed stop hook keeps e from the removal that follows in a down; a
// failed stop alone does not, as down removes such a replica by force.
func (s *Service) teardownStop(ctx context.Context, t *teardown, e cmdmanEntry) error {
	t.markStopped(e.Name)
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

// teardownRemove removes the replica e for t, by force should it still run,
// inside the remove hooks [Service.teardownHooks] returns for t.force.
func (s *Service) teardownRemove(ctx context.Context, t *teardown, e cmdmanEntry) error {
	r, hooks, err := s.teardownHooks(ctx, e, t.force, "remove")
	if err != nil {
		return err
	}
	removed, err := s.removeWithHooks(ctx, r, hooks, cmdman.RemoveRequest{
		Targets: []string{e.ID},
		Force:   true,
	})
	if removed {
		t.markRemoved(e.Name)
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

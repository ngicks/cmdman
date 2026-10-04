package compose

import (
	"context"

	"github.com/ngicks/cmdman/cmdman"
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
// for it to terminate, then runs stop_post.
func (s *Service) stopWithHooks(
	ctx context.Context,
	r hookReplica,
	hooks []LifecycleHook,
	id string,
) error {
	if _, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleStopPre); err != nil {
		return err
	}
	if err := s.stopForRecreate(ctx, id); err != nil {
		return err
	}
	_, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleStopPost)
	return err
}

// removeWithHooks runs remove_pre of hooks for r, removes the replica req
// targets, then runs remove_post. remove_post still finds the resource values
// of r: a holder outlives its replica.
func (s *Service) removeWithHooks(
	ctx context.Context,
	r hookReplica,
	hooks []LifecycleHook,
	req cmdman.RemoveRequest,
) error {
	if _, err := s.runLifecycleEvent(ctx, r, hooks, LifecycleRemovePre); err != nil {
		return err
	}
	results, err := s.svc.Remove(ctx, req)
	if err != nil {
		return err
	}
	for _, res := range results {
		if res.Err != nil {
			return res.Err
		}
	}
	_, err = s.runLifecycleEvent(ctx, r, hooks, LifecycleRemovePost)
	return err
}

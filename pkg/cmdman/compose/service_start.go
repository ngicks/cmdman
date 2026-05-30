package compose

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// StartOption configures a Start operation.
type StartOption struct {
	// CommandNames optionally narrows the start to a subset of commands. When
	// a compose file is loaded, dependencies of the named commands are pulled
	// in automatically. When no compose file is loaded, names match
	// LabelCommand directly with no dependency expansion.
	CommandNames []string
}

// StartResult is the aggregated result of a compose start operation.
type StartResult struct {
	Starts []StartOutcome
}

// StartOutcome records the result of starting a single compose command.
type StartOutcome struct {
	// Command is the compose command name.
	Command string
	// Err holds a non-nil error when the start failed.
	Err error
}

// Start starts commands selected by the project selection, honoring after.Condition
// when a compose file is loaded. With no compose file (project-only mode), starts
// the named commands concurrently with no dependency awareness.
func (s *Service) Start(
	ctx context.Context,
	selection ProjectSelection,
	opts StartOption,
) (*StartResult, error) {
	if selection.Spec != nil {
		return s.startWithSpec(ctx, selection, opts)
	}
	return s.startWithoutSpec(ctx, selection, opts)
}

func (s *Service) startWithSpec(
	ctx context.Context,
	selection ProjectSelection,
	opts StartOption,
) (*StartResult, error) {
	spec := *selection.Spec
	if err := validateCommandNames(opts.CommandNames, &spec, nil); err != nil {
		return nil, err
	}

	starts, err := s.reconcileStart(ctx, spec, opts.CommandNames)
	if err != nil {
		return nil, err
	}
	return &StartResult{Starts: starts}, nil
}

func (s *Service) startWithoutSpec(
	ctx context.Context,
	selection ProjectSelection,
	opts StartOption,
) (*StartResult, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	if err := validateCommandNames(opts.CommandNames, nil, entries); err != nil {
		return nil, err
	}
	if len(opts.CommandNames) > 0 {
		entries = filterByCommandNames(entries, opts.CommandNames)
	}
	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose start: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "start",
		)
		return &StartResult{}, nil
	}

	var (
		results  []StartOutcome
		eg, gctx = errgroup.WithContext(ctx)
		ch       = make(chan StartOutcome, len(entries))
	)
	for _, e := range entries {
		cmdName := ""
		if e.ConfigJSON != nil {
			cmdName = e.ConfigJSON.Labels[LabelCommand]
		}
		id, name := e.ID, cmdName
		state := e.State
		eg.Go(func() error {
			if state == model.EventTypeStarted || state == model.EventTypeStarting {
				ch <- StartOutcome{Command: name}
				return nil
			}
			if err := s.svc.Start(gctx, id); err != nil {
				wrapped := fmt.Errorf("start command %q (%s): %w", name, id, err)
				ch <- StartOutcome{Command: name, Err: wrapped}
				return nil
			}
			ch <- StartOutcome{Command: name}
			return nil
		})
	}
	_ = eg.Wait()
	close(ch)
	for o := range ch {
		results = append(results, o)
	}
	if results == nil {
		results = []StartOutcome{}
	}
	return &StartResult{Starts: results}, nil
}

package compose

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

// StartOption configures a Start operation.
type StartOption struct {
	// CommandNames optionally narrows the start to a subset of commands. When
	// a compose file or stored dependency graph is available, dependencies of
	// the named commands are pulled in automatically.
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

// Start starts commands selected by the project selection, honoring
// after.Condition from either a loaded compose file or stored compose labels.
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
	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose start: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "start",
		)
		return &StartResult{}, nil
	}

	spec, ok, err := reconstructProjectFromMeta(selection, entries)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf(
			"compose start: stored dependency graph is ambiguous; pass -f or --project-name",
		)
	}
	starts, err := s.reconcileStart(ctx, spec, opts.CommandNames)
	if err != nil {
		return nil, err
	}
	return &StartResult{Starts: starts}, nil
}

package compose

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

// Inspect returns the merged definition, runtime state, and exit history for
// the selected compose commands. With no command-name filter it inspects every
// command in the project; names narrow the set.
//
// Per resolved-decision 15, an empty project target set returns no outputs and
// logs a warning. Per-command inspect failures are aggregated into the returned
// error while successful outputs are still returned, so callers can render
// partial results.
func (s *Service) Inspect(
	ctx context.Context,
	selection ProjectSelection,
	commandNames []string,
) ([]*cmdman.InspectOutput, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	if err := validateCommandNames(commandNames, selection.Spec, entries); err != nil {
		return nil, err
	}
	if len(commandNames) > 0 {
		entries = filterByCommandNames(entries, commandNames)
	}

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn(
			"compose inspect: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "inspect",
		)
		return nil, nil
	}

	// Stable order by compose command name so the output is deterministic.
	slices.SortFunc(entries, func(a, b cmdmanEntry) int {
		if c := cmp.Compare(commandNameOf(a), commandNameOf(b)); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})

	outputs := make([]*cmdman.InspectOutput, 0, len(entries))
	var errs []error
	for _, entry := range entries {
		out, err := s.svc.Inspect(ctx, entry.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf(
				"inspect command %q (%s): %w", commandNameOf(entry), entry.ID, err))
			continue
		}
		outputs = append(outputs, out)
	}
	return outputs, errors.Join(errs...)
}

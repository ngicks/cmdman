package compose

import (
	"context"
	"fmt"
	"slices"

	"github.com/ngicks/cmdman/cmdman"
)

// ResolveCommandID resolves one replica of a compose command to the cmdman
// command ID backing it within the selected project. scaleIndex selects the
// replica (1-based); 0 means "the sole replica" and errors when the command has
// more than one, prompting the caller to disambiguate. Exported so other
// single-service operations (e.g. `compose attach`) can share the lookup.
func (s *Service) ResolveCommandID(
	ctx context.Context,
	selection ProjectSelection,
	commandName string,
	scaleIndex int,
) (string, error) {
	replicas, err := s.ResolveReplicas(ctx, selection, commandName)
	if err != nil {
		return "", err
	}
	if scaleIndex <= 0 {
		if len(replicas) != 1 {
			return "", fmt.Errorf(
				"compose command %q has %d replicas; select one with a scale index (1..%d)",
				commandName, len(replicas), len(replicas))
		}
		return replicas[0].ID, nil
	}
	if scaleIndex > len(replicas) {
		return "", fmt.Errorf(
			"compose command %q has no replica %d (scale is %d)",
			commandName, scaleIndex, len(replicas))
	}
	return replicas[scaleIndex-1].ID, nil
}

// ResolveReplicas returns the existing replicas of commandName within the
// selected project, ordered by 1-based scale index (index 1 first). It errors
// when the command is unknown or has no created replicas.
func (s *Service) ResolveReplicas(
	ctx context.Context,
	selection ProjectSelection,
	commandName string,
) ([]cmdmanEntry, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	if err := validateCommandNames([]string{commandName}, selection.Spec, entries); err != nil {
		return nil, err
	}

	matched := filterByCommandNames(entries, []string{commandName})
	if len(matched) == 0 {
		return nil, fmt.Errorf(
			"compose command %q not found in project (has it been created?)", commandName)
	}
	slices.SortFunc(matched, func(a, b cmdmanEntry) int {
		return scaleIndexOf(a) - scaleIndexOf(b)
	})
	return matched, nil
}

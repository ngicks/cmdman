package compose

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

// OpenAttachSession resolves a single compose command name to its backing
// cmdman command ID and opens an attach session against it.
//
// Unlike the project-wide compose operations, attach targets exactly one
// service: a PTY can only be bound to one terminal. commandName must be a known
// compose command with exactly one backing cmdman command.
func (s *Service) OpenAttachSession(
	ctx context.Context,
	selection ProjectSelection,
	commandName string,
) (*cmdman.Session, error) {
	id, err := s.ResolveCommandID(ctx, selection, commandName)
	if err != nil {
		return nil, err
	}
	return s.svc.OpenAttachSession(ctx, id)
}

// ResolveCommandID resolves a single compose command name to the cmdman command
// ID backing it within the selected project. Exported so other operations that
// take a single service name (e.g. `compose mux` leaf resolution) can share the
// lookup logic.
func (s *Service) ResolveCommandID(
	ctx context.Context,
	selection ProjectSelection,
	commandName string,
) (string, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return "", fmt.Errorf("list project commands: %w", err)
	}

	if err := validateCommandNames([]string{commandName}, selection.Spec, entries); err != nil {
		return "", err
	}

	matched := filterByCommandNames(entries, []string{commandName})
	switch len(matched) {
	case 0:
		return "", fmt.Errorf(
			"compose command %q not found in project (has it been created?)", commandName)
	case 1:
		return matched[0].ID, nil
	default:
		ids := make([]string, len(matched))
		for i, e := range matched {
			ids[i] = e.ID
		}
		return "", fmt.Errorf(
			"compose command %q maps to multiple commands %v; remove duplicates", commandName, ids)
	}
}

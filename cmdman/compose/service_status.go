package compose

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
)

// CommandRuntimeState is what one command of a compose project says about
// itself: the stored process state plus the runtime state its monitor holds for
// the current run. A command with no live monitor keeps the zero runtime fields.
//
// CLI-output type; see ProjectSummary for why it carries no json name tags.
type CommandRuntimeState struct {
	Command    string
	ID         string
	Name       string
	State      model.EventType
	Title      string `json:",omitzero"`
	Status     string `json:",omitzero"`
	Detail     string `json:",omitzero"`
	BellUnread bool
}

// Status reports the runtime state of the commands in the selected project,
// dialing their monitors in parallel. Unreachable monitors cost their command's
// runtime fields, not the listing (see [cmdman.Service.RuntimeStates]).
func (s *Service) Status(
	ctx context.Context,
	selection ProjectSelection,
	commandNames []string,
) ([]CommandRuntimeState, error) {
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

	runtime := s.runtimeStates(ctx, entries)

	states := make([]CommandRuntimeState, 0, len(entries))
	for _, entry := range entries {
		if entry.ConfigJSON == nil {
			continue
		}
		rs := runtime[entry.ID]
		states = append(states, CommandRuntimeState{
			Command:    entry.ConfigJSON.Labels[LabelCommand],
			ID:         entry.ID,
			Name:       entry.Name,
			State:      entry.State,
			Title:      rs.Title,
			Status:     rs.Status,
			Detail:     rs.Detail,
			BellUnread: rs.BellUnread,
		})
	}
	slices.SortFunc(states, func(a, b CommandRuntimeState) int {
		return cmp.Or(cmp.Compare(a.Command, b.Command), cmp.Compare(a.ID, b.ID))
	})
	return states, nil
}

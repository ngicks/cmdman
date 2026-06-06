package compose

import (
	"context"
	"fmt"
	"slices"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// ProjectSummary describes one compose project discovered from stored command
// labels.
type ProjectSummary struct {
	Project     string `json:"project"`
	WorkDir     string `json:"workdir"`
	ComposeFile string `json:"compose_file,omitzero"`
	Commands    int    `json:"commands"`
	Running     int    `json:"running"`
	Exited      int    `json:"exited"`
	Failed      int    `json:"failed"`
}

// CommandStatus describes one stored command in a compose project.
type CommandStatus struct {
	Command  string          `json:"command"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	State    model.EventType `json:"state"`
	ExitCode *int            `json:"exit_code,omitzero"`
	Argv     []string        `json:"argv,omitzero"`
}

// ListProjects returns every compose project known to the cmdman store.
func (s *Service) ListProjects(ctx context.Context) ([]ProjectSummary, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels: map[string]string{
			LabelVersion: LabelVersionValue,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list compose commands: %w", err)
	}

	byProject := map[string]*ProjectSummary{}
	var keys []string
	for _, entry := range entries {
		if entry.ConfigJSON == nil {
			continue
		}
		labels := entry.ConfigJSON.Labels
		project := labels[LabelProject]
		workDir := labels[LabelWorkdir]
		if project == "" || workDir == "" {
			continue
		}
		key := workDir + "\x00" + project
		summary := byProject[key]
		if summary == nil {
			summary = &ProjectSummary{
				Project:     project,
				WorkDir:     workDir,
				ComposeFile: labels[LabelFile],
			}
			byProject[key] = summary
			keys = append(keys, key)
		}
		summary.Commands++
		switch entry.State {
		case model.EventTypeRunning:
			summary.Running++
		case model.EventTypeExited:
			summary.Exited++
		case model.EventTypeFailed:
			summary.Failed++
		}
	}

	slices.SortFunc(keys, func(a, b string) int {
		pa, pb := byProject[a], byProject[b]
		if pa.Project < pb.Project {
			return -1
		}
		if pa.Project > pb.Project {
			return 1
		}
		if pa.WorkDir < pb.WorkDir {
			return -1
		}
		if pa.WorkDir > pb.WorkDir {
			return 1
		}
		return 0
	})

	summaries := make([]ProjectSummary, 0, len(keys))
	for _, key := range keys {
		summaries = append(summaries, *byProject[key])
	}
	return summaries, nil
}

// Ps lists commands in the selected compose project.
func (s *Service) Ps(
	ctx context.Context,
	selection ProjectSelection,
	commandNames []string,
) ([]CommandStatus, error) {
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

	statuses := make([]CommandStatus, 0, len(entries))
	for _, entry := range entries {
		if entry.ConfigJSON == nil {
			continue
		}
		statuses = append(statuses, CommandStatus{
			Command:  entry.ConfigJSON.Labels[LabelCommand],
			ID:       entry.ID,
			Name:     entry.Name,
			State:    entry.State,
			ExitCode: entry.ExitCode,
			Argv:     slices.Clone(entry.ConfigJSON.Argv),
		})
	}
	slices.SortFunc(statuses, func(a, b CommandStatus) int {
		if a.Command < b.Command {
			return -1
		}
		if a.Command > b.Command {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return statuses, nil
}

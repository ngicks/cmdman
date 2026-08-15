package core

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// CommandsLoadedMsg carries the result of a ListCommands load.
type CommandsLoadedMsg struct {
	Infos []CommandInfo
	Err   error
}

// ProjectsLoadedMsg carries the result of a ListProjects load.
type ProjectsLoadedMsg struct {
	Infos []ProjectInfo
	Err   error
}

// ProjectSwitchedMsg reports a switcher selection: the client either moved to
// the project's window or came back with the reason it did not.
type ProjectSwitchedMsg struct {
	Name string
	Err  error
}

// FrameHiddenMsg reports the collapse gesture's outcome (V8).
type FrameHiddenMsg struct {
	Err error
}

// ListCommandsCmd and ListProjectsCmd take their backend rather than a model so
// the single-widget model issues the very same loads as the full model.
func ListCommandsCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		infos, err := backend.ListCommands(ctx)
		return CommandsLoadedMsg{Infos: infos, Err: err}
	}
}

func ListProjectsCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		infos, err := backend.ListProjects(ctx)
		return ProjectsLoadedMsg{Infos: infos, Err: err}
	}
}

// SwitchProjectCmd and HideFrameCmd stand free of a model for the same reason
// the list commands do: the widget model issues them off the update loop with
// no model of its own to carry.
func SwitchProjectCmd(
	ctx context.Context,
	backend Backend,
	target SwitchTarget,
	name string,
) tea.Cmd {
	return func() tea.Msg {
		return ProjectSwitchedMsg{Name: name, Err: backend.SwitchToProject(ctx, target)}
	}
}

func HideFrameCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		return FrameHiddenMsg{Err: backend.HideFrame(ctx)}
	}
}

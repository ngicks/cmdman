package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// commandsLoadedMsg carries the result of a ListCommands load.
type commandsLoadedMsg struct {
	infos []CommandInfo
	err   error
}

// projectsLoadedMsg carries the result of a ListProjects load.
type projectsLoadedMsg struct {
	infos []ProjectInfo
	err   error
}

// actionDoneMsg reports completion of a lifecycle action.
type actionDoneMsg struct {
	verb string // "start", "stop", "restart", "remove"
	name string
	id   string
	err  error
}

// statusMsg sets a transient footer status message.
type statusMsg struct {
	text string
}

// projectSwitchedMsg reports a switcher selection: the client either moved to
// the project's window or came back with the reason it did not.
type projectSwitchedMsg struct {
	name string
	err  error
}

// frameHiddenMsg reports the collapse gesture's outcome (V8).
type frameHiddenMsg struct {
	err error
}

// switchProjectCmd and hideFrameCmd are package-level for the same reason the
// list commands are: the widget model issues them off the update loop with no
// model of its own to carry.
func switchProjectCmd(ctx context.Context, backend Backend, identity, name string) tea.Cmd {
	return func() tea.Msg {
		return projectSwitchedMsg{name: name, err: backend.SwitchToProject(ctx, identity)}
	}
}

func hideFrameCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		return frameHiddenMsg{err: backend.HideFrame(ctx)}
	}
}

func (m Model) bgCtx() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m Model) loadCommandsCmd() tea.Cmd { return listCommandsCmd(m.bgCtx(), m.backend) }

func (m Model) loadProjectsCmd() tea.Cmd { return listProjectsCmd(m.bgCtx(), m.backend) }

// listCommandsCmd and listProjectsCmd are package-level so the single-widget
// model issues the very same loads as the full model.
func listCommandsCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		infos, err := backend.ListCommands(ctx)
		return commandsLoadedMsg{infos: infos, err: err}
	}
}

func listProjectsCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		infos, err := backend.ListProjects(ctx)
		return projectsLoadedMsg{infos: infos, err: err}
	}
}

func (m Model) startCmd(id, name string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		err := backend.Start(ctx, id)
		return actionDoneMsg{verb: "start", name: name, id: id, err: err}
	}
}

func (m Model) stopCmd(id, name string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		err := backend.Stop(ctx, id)
		return actionDoneMsg{verb: "stop", name: name, id: id, err: err}
	}
}

func (m Model) restartCmd(id, name string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		err := backend.Restart(ctx, id)
		return actionDoneMsg{verb: "restart", name: name, id: id, err: err}
	}
}

func (m Model) removeCmd(id, name string, force bool) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		err := backend.Remove(ctx, id, force)
		return actionDoneMsg{verb: "remove", name: name, id: id, err: err}
	}
}

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

func (m Model) bgCtx() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

func (m Model) loadCommandsCmd() tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		infos, err := backend.ListCommands(ctx)
		return commandsLoadedMsg{infos: infos, err: err}
	}
}

func (m Model) loadProjectsCmd() tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
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

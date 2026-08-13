package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

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

func (m Model) loadCommandsCmd() tea.Cmd { return core.ListCommandsCmd(m.bgCtx(), m.backend) }

func (m Model) loadProjectsCmd() tea.Cmd { return core.ListProjectsCmd(m.bgCtx(), m.backend) }

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

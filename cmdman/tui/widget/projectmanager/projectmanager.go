// Package projectmanager implements the project-manager widget: the shortcut
// panel over one project's replica scale, shown-replica cycling and layout
// cycling. It is popup-summoned like the launcher and is not a frame component
// (D6).
//
// It is a standalone model rather than a panel facade: its keys are zoned
// (services list vs layouts list, with scale and cycle keys on top), so it
// shares no key handling with the docked widgets.
//
// The model here is the registration stub — the view and the backend calls land
// with the widget itself.
package projectmanager

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// Model is the project-manager widget. It is the whole program when
// Options.Widget names it, not a view inside another.
type Model struct {
	width, height int
	altScreen     bool
	// noQuit unbinds the quit gestures, as it does for every other widget (V6).
	noQuit bool

	quitting bool
}

// New constructs the project-manager model from the same options every other
// TUI mode takes. The program context the widget's backend calls will run under
// is not held yet: the stub issues none.
func New(_ context.Context, opts core.Options) Model {
	return Model{
		altScreen: opts.AltScreen,
		noQuit:    opts.NoQuit,
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "ctrl+d":
		if m.noQuit {
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.viewContent())
	v.AltScreen = m.altScreen
	return v
}

func (m Model) viewContent() string {
	if m.quitting {
		return ""
	}
	lines := []string{"project-manager", "", "not implemented yet"}
	if !m.noQuit {
		lines = append(lines, "", "q quits")
	}
	return strings.Join(lines, "\n")
}

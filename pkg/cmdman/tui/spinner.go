package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// spinnerInterval matches the compose TTY reporter so in-progress markers
// animate at the same cadence as `cmdman compose up`.
const spinnerInterval = 100 * time.Millisecond

// spinnerFrames is the braille spinner used for in-progress command states,
// identical to the compose progress reporter.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinnerTickMsg struct{}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// anyInProgress reports whether any command is mid-transition — a persisted
// `starting` state or a pending TUI action — i.e. whether the spinner should
// keep animating so a start cascade stays visible.
func (m *Model) anyInProgress() bool {
	for gi := range m.commands.groups {
		for _, c := range m.commands.groups[gi].commands {
			if c.pending != "" || c.state == model.EventTypeStarting {
				return true
			}
		}
	}
	return false
}

// maybeStartSpinner starts the animation ticker when work is in progress and it
// is not already running. The spinning flag prevents stacking tickers.
func (m *Model) maybeStartSpinner() tea.Cmd {
	if m.spinning || !m.anyInProgress() {
		return nil
	}
	m.spinning = true
	return spinnerTickCmd()
}

func (m Model) onSpinnerTick() (tea.Model, tea.Cmd) {
	m.spinner++
	if m.anyInProgress() {
		return m, spinnerTickCmd()
	}
	m.spinning = false
	return m, nil
}

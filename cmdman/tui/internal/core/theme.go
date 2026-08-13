package core

import (
	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/model"
)

// Charm-ish purple palette (256-color indices degrade gracefully).
var (
	ColorBorder = lipgloss.Color("63")  // indigo border
	ColorAccent = lipgloss.Color("99")  // purple titles/accents
	ColorOnAcc  = lipgloss.Color("231") // near-white text on a purple fill
)

// StyleActive dims secondary text — the "active" marker, placeholder lines, and
// the fallback shade for a terminal that never reported its own colors.
var StyleActive = lipgloss.NewStyle().Faint(true)

// Status-marker colors mirror the compose TTY reporter so the TUI shows the
// same indicators compose emits to the terminal.
var (
	StyleMarkProgress = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // in progress
	StyleMarkPending  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // created (ready)
	StyleMarkOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // running/exited
	StyleMarkErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // failed
)

func StatusStyle(state model.EventType, pending string) lipgloss.Style {
	if pending != "" || state == model.EventTypeStarting {
		return StyleMarkProgress
	}
	switch state {
	case model.EventTypeCreated:
		return StyleMarkPending
	case model.EventTypeRunning, model.EventTypeExited:
		return StyleMarkOK
	case model.EventTypeFailed:
		return StyleMarkErr
	default:
		return lipgloss.NewStyle()
	}
}

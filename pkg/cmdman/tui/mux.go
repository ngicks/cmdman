package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// muxDoneMsg reports the result of a mux cycle invocation.
type muxDoneMsg struct {
	project string
	err     error
}

// cycleMux handles the `c` key on the Compose tab. It cycles the selected
// project's mux layout via the existing compose mux path. The TUI keeps no
// layout state — mux owns its persisted window marker.
//
// Applying a layout rearranges the current tmux window. In popup mode the TUI
// lives in a popup, so this is safe and runs immediately. In direct mode the
// TUI occupies that window, so a confirmation warning is required first.
func (m Model) cycleMux() (tea.Model, tea.Cmd) {
	row, ok := m.compose.selectedComposeRow()
	if !ok {
		return m, nil
	}
	if !row.hasMux {
		m.status = fmt.Sprintf("%s has no mux section", row.name)
		return m, nil
	}
	if !m.popupMode {
		// Warn before rearranging the current window (which holds the TUI).
		m.popup = openMuxWarnPopup(row.name, row.path)
		return m, nil
	}
	m.status = fmt.Sprintf("cycling mux for %s…", row.name)
	return m, m.cycleMuxCmd(row.name, row.path)
}

func (m Model) cycleMuxCmd(name, composeFile string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		err := backend.CycleMux(ctx, name, composeFile)
		return muxDoneMsg{project: name, err: err}
	}
}

func (m Model) onMuxDone(msg muxDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("mux %s: %v", msg.project, msg.err)
		return m, nil
	}
	m.status = fmt.Sprintf("cycled mux layout for %s", msg.project)
	return m, nil
}

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// resolveEditor picks the editor launched by `e` on the Compose tab, honoring
// $VISUAL, then $EDITOR, then falling back to vim.
func resolveEditor() string {
	if v := strings.TrimSpace(os.Getenv("VISUAL")); v != "" {
		return v
	}
	if e := strings.TrimSpace(os.Getenv("EDITOR")); e != "" {
		return e
	}
	return "vim"
}

// editPathMsg carries the resolved compose-file path for an edit request.
type editPathMsg struct {
	project string
	path    string
	err     error
}

// editDoneMsg reports the editor handoff result.
type editDoneMsg struct {
	project string
	err     error
}

// editSelectedProject begins the `e` edit flow. The compose-file path is
// resolved off the update loop because never-run named projects carry no path
// yet, then the terminal is handed to the editor.
func (m Model) editSelectedProject() (tea.Model, tea.Cmd) {
	row, ok := m.compose.selectedComposeRow()
	if !ok {
		return m, nil
	}
	m.status = fmt.Sprintf("opening %s in %s…", row.name, resolveEditor())
	return m, m.resolveEditPathCmd(row.name, row.path)
}

func (m Model) resolveEditPathCmd(name, composeFile string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		path, err := backend.ComposeFilePath(ctx, name, composeFile)
		return editPathMsg{project: name, path: path, err: err}
	}
}

// onEditPath hands the terminal to the editor once the path is resolved.
// tea.ExecProcess wires the real terminal fds to the editor and restores them on
// exit.
func (m Model) onEditPath(msg editPathMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("edit %s: %v", msg.project, msg.err)
		return m, nil
	}
	name := msg.project
	cmd := exec.Command(resolveEditor(), msg.path)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editDoneMsg{project: name, err: err}
	})
}

func (m Model) onEditDone(msg editDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("edit %s: %v", msg.project, msg.err)
	} else {
		m.status = fmt.Sprintf("edited %s", msg.project)
	}
	// Redraw cleanly, re-query terminal size, and refresh after the handoff so an
	// edited compose file's new mux badge / modified time surface.
	return m, tea.Batch(tea.ClearScreen, tea.RequestWindowSize, m.loadProjectsCmd())
}

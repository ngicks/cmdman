package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// defLoadedMsg carries the result of a ProjectDefinition load.
type defLoadedMsg struct {
	project string
	text    string
	err     error
}

// openSelectedDefinition opens the read-only definition viewer for the selected
// compose project, loading its raw compose YAML off the update loop.
func (m Model) openSelectedDefinition() (tea.Model, tea.Cmd) {
	row, ok := m.compose.selectedComposeRow()
	if !ok {
		return m, nil
	}
	m.defViewer = defViewerState{open: true, project: row.name, loading: true}
	m.status = fmt.Sprintf("loading definition for %s…", row.name)
	return m, m.loadDefinitionCmd(row.name, row.path)
}

func (m Model) loadDefinitionCmd(name, composeFile string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		text, err := backend.ProjectDefinition(ctx, name, composeFile)
		return defLoadedMsg{project: name, text: text, err: err}
	}
}

func (m Model) onDefLoaded(msg defLoadedMsg) (tea.Model, tea.Cmd) {
	if !m.defViewer.open || m.defViewer.project != msg.project {
		return m, nil // viewer closed or switched to another project
	}
	m.defViewer.loading = false
	if msg.err != nil {
		m.defViewer.errMsg = msg.err.Error()
		m.status = fmt.Sprintf("definition %s: %v", msg.project, msg.err)
		return m, nil
	}
	// Expand tabs so they do not desync the box border alignment.
	m.defViewer.lines = strings.Split(strings.ReplaceAll(msg.text, "\t", "    "), "\n")
	m.defViewer.scroll = 0
	m.status = ""
	return m, nil
}

// onDefViewerKey handles keys while the definition viewer is open: scrolling and
// closing. It is a read-only overlay, so no other action is dispatched.
func (m Model) onDefViewerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.defViewer = defViewerState{}
		return m, nil
	case "j", "down":
		(&m).scrollDefViewer(1)
	case "k", "up":
		(&m).scrollDefViewer(-1)
	case "pgdown", " ":
		(&m).scrollDefViewer(m.defViewerPage())
	case "pgup":
		(&m).scrollDefViewer(-m.defViewerPage())
	}
	return m, nil
}

// scrollDefViewer moves the viewport top by delta, clamping to the content.
func (m *Model) scrollDefViewer(delta int) {
	maxTop := max(len(m.defViewer.lines)-m.defViewerPage(), 0)
	m.defViewer.scroll = min(max(m.defViewer.scroll+delta, 0), maxTop)
}

// defViewerSize returns the outer overlay box dimensions (including border).
func (m Model) defViewerSize() (w, h int) {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}
	return max(width-8, 20), max(height-6, 6)
}

// defViewerPage is the number of visible content lines (one screenful).
func (m Model) defViewerPage() int {
	_, h := m.defViewerSize()
	return max(h-2, 1)
}

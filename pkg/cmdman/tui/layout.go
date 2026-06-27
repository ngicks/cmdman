package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// layoutsLoadedMsg carries the result of a ListLayouts load.
type layoutsLoadedMsg struct {
	info LayoutsInfo
	err  error
}

// layoutDoneMsg reports the result of an ApplyLayout invocation.
type layoutDoneMsg struct {
	project string
	layout  string
	err     error
}

// maybeLoadLayoutsCmd returns a layout-load command when the Layout tab is the
// active tab, so entering the tab (re)loads its data; otherwise nil. tea.Batch
// drops nil commands, so it is safe to batch unconditionally.
func (m Model) maybeLoadLayoutsCmd() tea.Cmd {
	if m.active != TabLayout {
		return nil
	}
	return m.loadLayoutsCmd()
}

// loadLayoutsCmd loads the current project's mux layouts off the update loop.
// The Compose-tab selection is passed as the fallback project; the backend
// prefers the cwd-active mux project (D5).
func (m Model) loadLayoutsCmd() tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	var project, path string
	if row, ok := m.compose.selectedComposeRow(); ok {
		project, path = row.name, row.path
	}
	return func() tea.Msg {
		info, err := backend.ListLayouts(ctx, project, path)
		return layoutsLoadedMsg{info: info, err: err}
	}
}

func (m Model) onLayoutsLoaded(msg layoutsLoadedMsg) (tea.Model, tea.Cmd) {
	m.layout.loaded = true
	if msg.err != nil {
		m.status = fmt.Sprintf("layout list error: %v", msg.err)
		m.layout.rows = nil
		m.layout.current = -1
		m.layout.selected = 0
		return m, nil
	}
	rows := make([]layoutRow, 0, len(msg.info.Names))
	for _, n := range msg.info.Names {
		rows = append(rows, layoutRow{name: n})
	}
	m.layout.rows = rows
	m.layout.project = msg.info.Project
	m.layout.path = msg.info.Path
	m.layout.current = msg.info.Current
	// Default the selection to the displayed layout so focus lands on it.
	sel := msg.info.Current
	if sel < 0 || sel >= len(rows) {
		sel = 0
	}
	m.layout.selected = sel
	return m, nil
}

// onLayoutKey handles keys on the Layout tab: j/k move the selection, enter
// applies the selected layout, r reloads.
func (m Model) onLayoutKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.layout.moveSelection(1)
		return m, nil
	case "k", "up":
		m.layout.moveSelection(-1)
		return m, nil
	case "enter":
		return m.applySelectedLayout()
	case "r":
		m.status = "refreshing layouts…"
		return m, m.loadLayoutsCmd()
	}
	return m, nil
}

// applySelectedLayout applies the selected layout (D6): it applies to the
// running dashboard and starts one at that layout when none is running.
//
// Applying rearranges the multiplexer window. In direct mode the TUI occupies
// that window, so a warning confirmation is required first (R2); in popup mode
// the dashboard is in the underlying window, so it applies immediately.
func (m Model) applySelectedLayout() (tea.Model, tea.Cmd) {
	if len(m.layout.rows) == 0 {
		m.status = "no layouts to apply"
		return m, nil
	}
	if m.layout.selected < 0 || m.layout.selected >= len(m.layout.rows) {
		return m, nil
	}
	name := m.layout.rows[m.layout.selected].name
	if !m.popupMode {
		m.popup = openLayoutWarnPopup(m.layout.project, m.layout.path, name)
		return m, nil
	}
	m.status = fmt.Sprintf("applying layout %s…", name)
	return m, m.applyLayoutCmd(m.layout.project, m.layout.path, name)
}

func (m Model) applyLayoutCmd(project, composeFile, layoutName string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		err := backend.ApplyLayout(ctx, project, composeFile, layoutName)
		return layoutDoneMsg{project: project, layout: layoutName, err: err}
	}
}

func (m Model) onLayoutDone(msg layoutDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("layout %s: %v", msg.layout, msg.err)
		return m, nil
	}
	m.status = fmt.Sprintf("applied layout %s for %s", msg.layout, msg.project)
	// Reload so the current marker reflects the just-applied layout (relevant in
	// popup mode, where the TUI survives the rearrange).
	return m, m.loadLayoutsCmd()
}

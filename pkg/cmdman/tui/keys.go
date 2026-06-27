package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// onKey routes a key press based on the current modal state.
func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Hard exits are always available but undocumented (q is the advertised
	// quit key). During attach these are forwarded to the remote command by
	// the attach implementation, not handled here.
	switch msg.String() {
	case "ctrl+c", "ctrl+d":
		m.quitting = true
		return m, tea.Quit
	}

	if m.popup.open() {
		return m.onPopupKey(msg)
	}
	if m.helpOpen {
		return m.onHelpKey(msg)
	}
	if m.defViewer.open {
		return m.onDefViewerKey(msg)
	}
	if m.activeFiltering() {
		return m.onFilterKey(msg)
	}
	return m.onNormalKey(msg)
}

// activeFiltering reports whether the active tab's filter input has focus.
func (m Model) activeFiltering() bool {
	switch m.active {
	case TabCommands:
		return m.commands.filtering
	case TabCompose:
		return m.compose.filtering
	default:
		return false
	}
}

func (m Model) onPopupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.popup = popupState{}
		return m, nil
	case "left", "right", "h", "l", "tab", "shift+tab":
		m.popup.toggleChoice()
		return m, nil
	case "enter":
		return m.confirmPopup()
	}
	return m, nil
}

func (m Model) confirmPopup() (tea.Model, tea.Cmd) {
	p := m.popup
	m.popup = popupState{}
	if !p.confirmed() {
		m.status = "cancelled"
		return m, nil
	}
	switch p.kind {
	case popupAttach:
		m.status = fmt.Sprintf("attaching to %s…", p.command)
		return m, m.startAttach(p.targetID, p.command)
	case popupRemove, popupForceRemove:
		force := p.kind == popupForceRemove
		m.setPending(p.targetID, "removing")
		return m, m.removeCmd(p.targetID, p.command, force)
	case popupMuxWarn:
		// A carried layout name means "apply this layout" (Layout tab); an empty
		// one means "cycle to the next layout" (Compose tab `c`).
		if p.layout != "" {
			m.status = fmt.Sprintf("applying layout %s…", p.layout)
			return m, m.applyLayoutCmd(p.project, p.path, p.layout)
		}
		m.status = fmt.Sprintf("cycling mux for %s…", p.project)
		return m, m.cycleMuxCmd(p.project, p.path)
	case popupComposeUp:
		m.status = fmt.Sprintf("compose up %s…", p.project)
		return m, m.composeUpCmd(p.project, p.path)
	}
	return m, nil
}

func (m Model) onHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "?", "esc":
		m.helpOpen = false
		return m, nil
	}
	return m, nil
}

// onFilterKey edits the active tab's filter text. Single-key lifecycle/quit
// bindings are inert while the filter input has focus.
func (m Model) onFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()
	switch key.Code {
	case tea.KeyEscape:
		m.setFiltering(false)
		return m, nil
	case tea.KeyEnter:
		// Accept the filter and leave the input focused on the list.
		m.setFiltering(false)
		return m, nil
	case tea.KeyBackspace:
		m.editFilter(func(s string) string {
			if s == "" {
				return s
			}
			r := []rune(s)
			return string(r[:len(r)-1])
		})
		return m, nil
	}
	// v2 carries printable input (letters, digits, space, …) in Key.Text;
	// special keys leave it empty, so this also covers what KeySpace/KeyRunes
	// handled in v1.
	if key.Text != "" {
		m.appendFilter(key.Text)
	}
	return m, nil
}

func (m *Model) setFiltering(v bool) {
	switch m.active {
	case TabCommands:
		m.commands.filtering = v
	case TabCompose:
		m.compose.filtering = v
	}
}

func (m *Model) editFilter(fn func(string) string) {
	switch m.active {
	case TabCommands:
		m.commands.filter = fn(m.commands.filter)
		m.commands.clampSelection()
	case TabCompose:
		m.compose.filter = fn(m.compose.filter)
		if m.compose.selected >= len(m.compose.visibleRows()) {
			m.compose.selected = 0
		}
	}
}

func (m *Model) appendFilter(s string) {
	m.editFilter(func(cur string) string { return cur + s })
}

// onNormalKey handles keys outside any modal/filter state.
func (m Model) onNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "?":
		m.helpOpen = true
		return m, nil
	case "tab":
		m.active = Tab((int(m.active) + 1) % NumTabs())
		m.status = ""
		return m, m.maybeLoadLayoutsCmd()
	case "shift+tab":
		m.active = Tab((int(m.active) + NumTabs() - 1) % NumTabs())
		m.status = ""
		return m, m.maybeLoadLayoutsCmd()
	case "/":
		m.setFiltering(true)
		return m, nil
	}

	switch m.active {
	case TabCommands:
		return m.onCommandsKey(msg)
	case TabCompose:
		return m.onComposeKey(msg)
	case TabLayout:
		return m.onLayoutKey(msg)
	}
	return m, nil
}

func (m Model) onCommandsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.commands.moveSelection(1)
		return m, nil
	case "k", "up":
		m.commands.moveSelection(-1)
		return m, nil
	case "h", "left":
		return m.commandsFoldOrFocus(true), nil
	case "l", "right":
		return m.commandsFoldOrFocus(false), nil
	case "enter":
		// On a project row, enter toggles fold. On a command row it is a
		// no-op: lifecycle actions are explicit and never toggle on enter.
		if r, ok := m.commands.selectedRow(); ok && r.kind == visProject {
			m.commands.setFolded(r.group, !m.commands.folded(r.group))
			m.commands.clampSelection()
		}
		return m, nil
	case "s":
		return m.startSelected()
	case "S":
		return m.stopSelected()
	case "r":
		return m.restartSelected()
	case "a":
		return m.attachSelected()
	case "x":
		return m.removeSelected()
	}
	return m, nil
}

// commandsFoldOrFocus implements h/l: on a project row it folds (h) or unfolds
// (l); on a command row it moves pane focus left (list) or right (preview).
func (m Model) commandsFoldOrFocus(left bool) Model {
	r, ok := m.commands.selectedRow()
	if !ok {
		return m
	}
	if r.kind == visProject {
		m.commands.setFolded(r.group, left)
		m.commands.clampSelection()
		return m
	}
	if left {
		m.commands.focus = paneList
	} else {
		m.commands.focus = panePreview
	}
	return m
}

func (m Model) startSelected() (tea.Model, tea.Cmd) {
	c, ok := m.commands.selectedCommand()
	if !ok {
		m.status = "select a command"
		return m, nil
	}
	if c.state == model.EventTypeRunning || c.state == model.EventTypeStarting {
		m.status = fmt.Sprintf("%s is already running", c.name)
		return m, nil
	}
	if p := m.pendingOf(c.id); p != "" {
		m.status = fmt.Sprintf("%s is already %s", c.name, p)
		return m, nil
	}
	m.setPending(c.id, "starting")
	return m, m.startCmd(c.id, c.name)
}

func (m Model) stopSelected() (tea.Model, tea.Cmd) {
	c, ok := m.commands.selectedCommand()
	if !ok {
		m.status = "select a command"
		return m, nil
	}
	if c.state != model.EventTypeRunning && c.state != model.EventTypeStarting {
		m.status = fmt.Sprintf("%s is not running", c.name)
		return m, nil
	}
	if p := m.pendingOf(c.id); p != "" {
		m.status = fmt.Sprintf("%s is already %s", c.name, p)
		return m, nil
	}
	m.setPending(c.id, "stopping")
	return m, m.stopCmd(c.id, c.name)
}

func (m Model) restartSelected() (tea.Model, tea.Cmd) {
	c, ok := m.commands.selectedCommand()
	if !ok {
		m.status = "select a command"
		return m, nil
	}
	if p := m.pendingOf(c.id); p != "" {
		m.status = fmt.Sprintf("%s is already %s", c.name, p)
		return m, nil
	}
	m.setPending(c.id, "restarting")
	return m, m.restartCmd(c.id, c.name)
}

func (m Model) attachSelected() (tea.Model, tea.Cmd) {
	c, ok := m.commands.selectedCommand()
	if !ok {
		m.status = "select a command"
		return m, nil
	}
	m.popup = openAttachPopup(c.project, c.name, c.id)
	return m, nil
}

func (m Model) removeSelected() (tea.Model, tea.Cmd) {
	c, ok := m.commands.selectedCommand()
	if !ok {
		m.status = "select a command"
		return m, nil
	}
	running := c.state == model.EventTypeRunning || c.state == model.EventTypeStarting
	m.popup = openRemovePopup(c.project, c.name, c.id, running)
	return m, nil
}

func (m Model) onComposeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.compose.moveSelection(1)
		return m, nil
	case "k", "up":
		m.compose.moveSelection(-1)
		return m, nil
	case "enter":
		return m.openSelectedDefinition()
	case "e":
		return m.editSelectedProject()
	case "a":
		return m.composeUpSelected()
	case "r":
		m.status = "refreshing projects…"
		return m, m.loadProjectsCmd()
	case "c":
		return m.cycleMux()
	}
	return m, nil
}

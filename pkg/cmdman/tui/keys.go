package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

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
	if m.activeFiltering() {
		return m.onFilterKey(msg)
	}
	return m.onNormalKey(msg)
}

// activeFiltering reports whether the active tab's filter input has focus.
func (m Model) activeFiltering() bool {
	if m.active == tabCommands {
		return m.commands.filtering
	}
	return m.compose.filtering
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
		// The terminal handoff is wired by the runtime layer; the core shell
		// only owns the confirmation.
		m.status = fmt.Sprintf("attach %s: not available yet", p.command)
		return m, nil
	case popupRemove, popupForceRemove:
		force := p.kind == popupForceRemove
		m.setPending(p.targetID, "removing")
		return m, m.removeCmd(p.targetID, p.command, force)
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
	switch msg.Type {
	case tea.KeyEsc:
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
	case tea.KeySpace:
		m.appendFilter(" ")
		return m, nil
	case tea.KeyRunes:
		m.appendFilter(string(msg.Runes))
		return m, nil
	}
	return m, nil
}

func (m *Model) setFiltering(v bool) {
	if m.active == tabCommands {
		m.commands.filtering = v
	} else {
		m.compose.filtering = v
	}
}

func (m *Model) editFilter(fn func(string) string) {
	if m.active == tabCommands {
		m.commands.filter = fn(m.commands.filter)
		m.commands.clampSelection()
	} else {
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
		m.active = tab((int(m.active) + 1) % numTabs)
		m.status = ""
		return m, nil
	case "shift+tab":
		m.active = tab((int(m.active) + numTabs - 1) % numTabs)
		m.status = ""
		return m, nil
	case "/":
		m.setFiltering(true)
		return m, nil
	}

	if m.active == tabCommands {
		return m.onCommandsKey(msg)
	}
	return m.onComposeKey(msg)
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
	if c.state == model.EventTypeStarted || c.state == model.EventTypeStarting {
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
	if c.state != model.EventTypeStarted && c.state != model.EventTypeStarting {
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
	running := c.state == model.EventTypeStarted || c.state == model.EventTypeStarting
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
		return m.openSelectedProject()
	case "r":
		m.status = "refreshing projects…"
		return m, m.loadProjectsCmd()
	case "c":
		// Mux cycle is wired by the mux layer; the core shell reports it is
		// not yet available.
		row, ok := m.compose.selectedComposeRow()
		if !ok {
			return m, nil
		}
		if !row.hasMux {
			m.status = fmt.Sprintf("%s has no mux section", row.name)
			return m, nil
		}
		m.status = "mux cycle: not available yet"
		return m, nil
	case "l":
		m.status = "Specific layout selection is not available yet; use c to cycle layouts."
		return m, nil
	}
	return m, nil
}

// openSelectedProject switches to the Commands tab and selects the chosen
// project, unfolding it.
func (m Model) openSelectedProject() (tea.Model, tea.Cmd) {
	row, ok := m.compose.selectedComposeRow()
	if !ok {
		return m, nil
	}
	m.active = tabCommands
	for gi := range m.commands.groups {
		if m.commands.groups[gi].name == row.name {
			m.commands.setFolded(gi, false)
			// Select the project's header row.
			vis := m.commands.visibleRows()
			for i, vr := range vis {
				if vr.kind == visProject && vr.group == gi {
					m.commands.selected = i
					break
				}
			}
			m.status = ""
			return m, nil
		}
	}
	m.status = fmt.Sprintf("project %q has no commands yet", row.name)
	return m, nil
}

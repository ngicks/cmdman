package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadCommandsCmd(), m.loadProjectsCmd(), m.subscribeEventsCmd())
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case commandsLoadedMsg:
		nm, cmd := m.onCommandsLoaded(msg)
		scmd := (&nm).maybeStartSpinner()
		return nm, tea.Batch(cmd, scmd)
	case projectsLoadedMsg:
		return m.onProjectsLoaded(msg), nil
	case actionDoneMsg:
		return m.onActionDone(msg)
	case eventsSubscribedMsg:
		return m.onEventsSubscribed(msg)
	case eventSignalMsg:
		return m.onEventSignal(msg)
	case reloadTickMsg:
		return m.onReloadTick(msg)
	case previewOpenedMsg:
		return m.onPreviewOpened(msg)
	case previewLineMsg:
		return m.onPreviewLine(msg)
	case attachDoneMsg:
		return m.onAttachDone(msg)
	case muxDoneMsg:
		return m.onMuxDone(msg)
	case spinnerTickMsg:
		return m.onSpinnerTick()
	case statusMsg:
		m.status = msg.text
		return m, nil
	case tea.KeyMsg:
		nm, cmd := m.onKey(msg)
		m2 := nm.(Model)
		// Reconcile the preview after any key that may have moved the selected
		// command, and (re)start the spinner if a key kicked off an action.
		pcmd := (&m2).reconcilePreview()
		scmd := (&m2).maybeStartSpinner()
		return m2, tea.Batch(cmd, pcmd, scmd)
	}
	return m, nil
}

func (m Model) onCommandsLoaded(msg commandsLoadedMsg) (Model, tea.Cmd) {
	if m.backend != nil {
		m.cwd = m.backend.Cwd()
	}
	if msg.err != nil {
		m.status = fmt.Sprintf("list error: %v", msg.err)
		return m, nil
	}
	prevID, _ := m.commands.selectedCommand()
	m.setGroups(groupFromInfos(msg.infos))
	if prevID.id != "" {
		m.selectCommandByID(prevID.id)
	}
	pcmd := (&m).reconcilePreview()
	return m, pcmd
}

func (m Model) onProjectsLoaded(msg projectsLoadedMsg) Model {
	if m.backend != nil {
		m.cwd = m.backend.Cwd()
	}
	if msg.err != nil {
		m.status = fmt.Sprintf("project list error: %v", msg.err)
		return m
	}
	rows := make([]composeRow, 0, len(msg.infos))
	for _, p := range msg.infos {
		rows = append(rows, composeRow{
			name:     p.Name,
			path:     p.Path,
			workdir:  p.Workdir,
			commands: p.Commands,
			running:  p.Running,
			exited:   p.Exited,
			failed:   p.Failed,
			hasMux:   p.HasMux,
			modified: p.Modified,
		})
	}
	m.setComposeRows(rows)
	return m
}

func (m Model) onActionDone(msg actionDoneMsg) (tea.Model, tea.Cmd) {
	// Clear any pending marker for the affected command.
	m.clearPending(msg.id)
	if msg.err != nil {
		m.status = fmt.Sprintf("%s %s: %v", msg.verb, msg.name, msg.err)
	} else {
		m.status = fmt.Sprintf("%s %s: ok", msg.verb, msg.name)
	}
	// Refresh both views after a lifecycle action completes.
	return m, tea.Batch(m.loadCommandsCmd(), m.loadProjectsCmd())
}

// selectCommandByID moves the Commands-tab selection to the visible row for the
// given command id, when present.
func (m *Model) selectCommandByID(id string) {
	rows := m.commands.visibleRows()
	for i, r := range rows {
		if r.kind == visCommand && m.commands.groups[r.group].commands[r.cmd].id == id {
			m.commands.selected = i
			return
		}
	}
	m.commands.clampSelection()
}

// setPending marks the command with id as having a pending action label.
func (m *Model) setPending(id, label string) {
	for gi := range m.commands.groups {
		for ci := range m.commands.groups[gi].commands {
			if m.commands.groups[gi].commands[ci].id == id {
				m.commands.groups[gi].commands[ci].pending = label
				return
			}
		}
	}
}

// clearPending clears the pending marker for the command with id.
func (m *Model) clearPending(id string) {
	for gi := range m.commands.groups {
		for ci := range m.commands.groups[gi].commands {
			if m.commands.groups[gi].commands[ci].id == id {
				m.commands.groups[gi].commands[ci].pending = ""
				return
			}
		}
	}
}

// pendingOf reports the pending label for the command with id, if any.
func (m *Model) pendingOf(id string) string {
	for gi := range m.commands.groups {
		for ci := range m.commands.groups[gi].commands {
			if m.commands.groups[gi].commands[ci].id == id {
				return m.commands.groups[gi].commands[ci].pending
			}
		}
	}
	return ""
}

package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadCommandsCmd(),
		m.loadProjectsCmd(),
		m.subscribeEventsCmd(),
		armRuntimeWatchCmd(),
	)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// The terminal-view emulator is sized to the command's PTY (not the pane);
		// the preview crops it on render, so a window resize needs no emulator work.
		return m, nil
	case core.CommandsLoadedMsg:
		nm, cmd := m.onCommandsLoaded(msg)
		scmd := (&nm).maybeStartSpinner()
		return nm, tea.Batch(cmd, scmd)
	case core.ProjectsLoadedMsg:
		return m.onProjectsLoaded(msg), nil
	case actionDoneMsg:
		return m.onActionDone(msg)
	case core.EventsSubscribedMsg:
		return m.onEventsSubscribed(msg)
	case core.EventSignalMsg:
		return m.onEventSignal(msg)
	case core.ReloadTickMsg:
		return m.onReloadTick(msg)
	case runtimeWatchReadyMsg:
		return m, core.WaitRuntimeUpdateCmd(m.watcher)
	case core.RuntimeUpdateMsg:
		return m.onRuntimeUpdate(msg)
	case previewOpenedMsg:
		return m.onPreviewOpened(msg)
	case previewLineMsg:
		return m.onPreviewLine(msg)
	case rawOpenedMsg:
		return m.onRawOpened(msg)
	case rawTickMsg:
		return m.onRawTick(msg)
	case rawClosedMsg:
		return m.onRawClosed(msg)
	case attachDoneMsg:
		return m.onAttachDone(msg)
	case muxDoneMsg:
		return m.onMuxDone(msg)
	case defLoadedMsg:
		return m.onDefLoaded(msg)
	case editPathMsg:
		return m.onEditPath(msg)
	case editDoneMsg:
		return m.onEditDone(msg)
	case composeUpOpenedMsg:
		return m.onComposeUpOpened(msg)
	case composeUpEventMsg:
		return m.onComposeUpEvent(msg)
	case composeUpDoneMsg:
		return m.onComposeUpDone(msg)
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

func (m Model) onCommandsLoaded(msg core.CommandsLoadedMsg) (Model, tea.Cmd) {
	if m.backend != nil {
		m.cwd = m.backend.Cwd()
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf("list error: %v", msg.Err)
		return m, nil
	}
	prevID, _ := m.commands.selectedCommand()
	// The list is what says which commands exist, so it is also what the held
	// streams are reconciled against. The ids it reports dropping are not read:
	// the merge below evicts every one of them and, beyond them, the entries
	// whose stream ended on its own — those the watcher already forgot, so a
	// reconcile can no longer name them.
	m.watcher.Reconcile(m.bgCtx(), m.backend, msg.Infos)
	groups := core.GroupFromInfos(msg.Infos)
	m.mergeRuntime(groups)
	m.setGroups(groups)
	if prevID.ID != "" {
		m.selectCommandByID(prevID.ID)
	}
	pcmd := (&m).reconcilePreview()
	return m, pcmd
}

func (m Model) onProjectsLoaded(msg core.ProjectsLoadedMsg) Model {
	if m.backend != nil {
		m.cwd = m.backend.Cwd()
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf("project list error: %v", msg.Err)
		return m
	}
	rows := make([]composeRow, 0, len(msg.Infos))
	for _, p := range msg.Infos {
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
	m.clearPending(msg.id)
	if msg.err != nil {
		m.status = fmt.Sprintf("%s %s: %v", msg.verb, msg.name, msg.err)
	} else {
		m.status = fmt.Sprintf("%s %s: ok", msg.verb, msg.name)
	}
	return m, tea.Batch(m.loadCommandsCmd(), m.loadProjectsCmd())
}

// selectCommandByID moves the Commands-tab selection to the visible row for the
// given command id, when present.
func (m *Model) selectCommandByID(id string) {
	rows := m.commands.visibleRows()
	for i, r := range rows {
		if r.kind == visCommand && m.commands.groups[r.group].Commands[r.cmd].ID == id {
			m.commands.selected = i
			return
		}
	}
	m.commands.clampSelection()
}

// setPending marks the command with id as having a pending action label.
func (m *Model) setPending(id, label string) {
	for gi := range m.commands.groups {
		for ci := range m.commands.groups[gi].Commands {
			if m.commands.groups[gi].Commands[ci].ID == id {
				m.commands.groups[gi].Commands[ci].Pending = label
				return
			}
		}
	}
}

// clearPending clears the pending marker for the command with id.
func (m *Model) clearPending(id string) {
	for gi := range m.commands.groups {
		for ci := range m.commands.groups[gi].Commands {
			if m.commands.groups[gi].Commands[ci].ID == id {
				m.commands.groups[gi].Commands[ci].Pending = ""
				return
			}
		}
	}
}

// pendingOf reports the pending label for the command with id, if any.
func (m *Model) pendingOf(id string) string {
	for gi := range m.commands.groups {
		for ci := range m.commands.groups[gi].Commands {
			if m.commands.groups[gi].Commands[ci].ID == id {
				return m.commands.groups[gi].Commands[ci].Pending
			}
		}
	}
	return ""
}

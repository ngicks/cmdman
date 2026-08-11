package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// composeUpOpenedMsg reports the result of opening a compose-up stream.
type composeUpOpenedMsg struct {
	project string
	stream  ComposeUpStream
	err     error
}

// composeUpEventMsg carries one per-service progress event from a running
// compose up.
type composeUpEventMsg struct {
	project string
	event   ComposeUpEvent
}

// composeUpDoneMsg reports that a compose up reached its terminal phase (the
// event stream closed); err is the operation-level error, if any.
type composeUpDoneMsg struct {
	project string
	err     error
}

// composeUpSelected handles `a` on the Compose tab: it opens the confirmation
// popup for the selected project. The run itself is dispatched on confirm.
func (m Model) composeUpSelected() (tea.Model, tea.Cmd) {
	if m.composeUp.active {
		m.status = "compose up already running"
		return m, nil
	}
	row, ok := m.compose.selectedComposeRow()
	if !ok {
		m.status = "select a project"
		return m, nil
	}
	m.popup = openComposeUpPopup(row.name, row.path)
	return m, nil
}

// composeUpCmd opens the compose-up stream off the update loop. The stream's
// progress events are forwarded back into the model as tea messages, mirroring
// the preview/events pipelines in runtime.go.
func (m Model) composeUpCmd(project, composeFile string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		stream, err := backend.ComposeUp(ctx, project, composeFile)
		return composeUpOpenedMsg{project: project, stream: stream, err: err}
	}
}

// waitComposeUpCmd pulls the next progress event, turning the stream into a
// repeated tea.Cmd. A closed channel becomes a composeUpDoneMsg carrying the
// operation-level error.
func waitComposeUpCmd(stream ComposeUpStream, project string) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-stream.Events()
		if !ok {
			return composeUpDoneMsg{project: project, err: stream.Err()}
		}
		return composeUpEventMsg{project: project, event: ev}
	}
}

func (m Model) onComposeUpOpened(msg composeUpOpenedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("compose up %s: %v", msg.project, msg.err)
		return m, nil
	}
	m.composeUp = composeUpState{
		active:  true,
		project: msg.project,
		marks:   map[string]composeUpMark{},
		stream:  msg.stream,
	}
	scmd := (&m).maybeStartSpinner()
	return m, tea.Batch(waitComposeUpCmd(msg.stream, msg.project), scmd)
}

func (m Model) onComposeUpEvent(msg composeUpEventMsg) (tea.Model, tea.Cmd) {
	if !m.composeUp.active || m.composeUp.project != msg.project {
		return m, nil // stale stream for a closed/replaced overlay
	}
	(&m).recordComposeUpEvent(msg.event)
	scmd := (&m).maybeStartSpinner()
	return m, tea.Batch(waitComposeUpCmd(m.composeUp.stream, msg.project), scmd)
}

func (m Model) onComposeUpDone(msg composeUpDoneMsg) (tea.Model, tea.Cmd) {
	if !m.composeUp.active || m.composeUp.project != msg.project {
		return m, nil // stale terminal for a closed/replaced overlay
	}
	summary := m.composeUpSummary(msg.err)
	if m.composeUp.stream != nil {
		_ = m.composeUp.stream.Close()
	}
	m.composeUp = composeUpState{}
	m.status = summary
	return m, nil
}

// recordComposeUpEvent folds one event into the overlay state, tracking the
// service order so the overlay renders deterministically.
func (m *Model) recordComposeUpEvent(ev ComposeUpEvent) {
	if ev.Command == "" {
		return
	}
	if _, seen := m.composeUp.marks[ev.Command]; !seen {
		m.composeUp.order = append(m.composeUp.order, ev.Command)
	}
	m.composeUp.marks[ev.Command] = composeUpMark{
		phase:    ev.Phase,
		terminal: ev.Terminal,
		failed:   ev.Failed,
	}
}

// composeUpSummary collapses the overlay to a one-line footer summary. An
// operation-level error wins; otherwise it counts terminal-failure marks.
func (m Model) composeUpSummary(err error) string {
	if err != nil {
		return fmt.Sprintf("compose up %s: %v", m.composeUp.project, err)
	}
	var ok, failed int
	for _, name := range m.composeUp.order {
		if m.composeUp.marks[name].failed {
			failed++
		} else {
			ok++
		}
	}
	if failed > 0 {
		return fmt.Sprintf("compose up %s: %d ok, %d failed", m.composeUp.project, ok, failed)
	}
	return fmt.Sprintf("compose up %s: %d services up", m.composeUp.project, ok)
}

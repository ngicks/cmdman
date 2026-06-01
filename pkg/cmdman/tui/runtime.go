package tui

import (
	"fmt"
	"io"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// debounceInterval coalesces a burst of lifecycle events into a single re-list.
const debounceInterval = 150 * time.Millisecond

// previewMaxLines caps the in-memory preview buffer.
const previewMaxLines = 5000

// --- events / debounced re-list --------------------------------------------

type eventsSubscribedMsg struct {
	stream EventStream
	err    error
}

type eventSignalMsg struct {
	err    error
	closed bool
}

type reloadTickMsg struct {
	gen int
}

func (m Model) subscribeEventsCmd() tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		stream, err := backend.Events(ctx)
		return eventsSubscribedMsg{stream: stream, err: err}
	}
}

func waitEventCmd(stream EventStream) tea.Cmd {
	return func() tea.Msg {
		sig, ok := <-stream.Signals()
		if !ok {
			return eventSignalMsg{closed: true}
		}
		return eventSignalMsg{err: sig.Err}
	}
}

func debounceCmd(gen int) tea.Cmd {
	return tea.Tick(debounceInterval, func(time.Time) tea.Msg {
		return reloadTickMsg{gen: gen}
	})
}

func (m Model) onEventsSubscribed(msg eventsSubscribedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = fmt.Sprintf("events: %v", msg.err)
		return m, nil
	}
	m.events = msg.stream
	return m, waitEventCmd(msg.stream)
}

func (m Model) onEventSignal(msg eventSignalMsg) (tea.Model, tea.Cmd) {
	if msg.closed {
		m.events = nil
		return m, nil // subscription ended; stop waiting
	}
	if msg.err != nil {
		// Surface event-tail errors without closing the TUI; keep listening.
		m.status = fmt.Sprintf("events: %v", msg.err)
		return m, waitEventCmd(m.events)
	}
	// A lifecycle change occurred: bump the debounce generation and schedule a
	// re-list, while continuing to listen for further events.
	m.reloadGen++
	return m, tea.Batch(waitEventCmd(m.events), debounceCmd(m.reloadGen))
}

func (m Model) onReloadTick(msg reloadTickMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.reloadGen {
		return m, nil // a newer event arrived; let the latest tick win
	}
	return m, tea.Batch(m.loadCommandsCmd(), m.loadProjectsCmd())
}

// --- preview ----------------------------------------------------------------

type previewOpenedMsg struct {
	cmdID  string
	stream LogStream
	err    error
}

type previewLineMsg struct {
	cmdID  string
	line   string
	err    error
	closed bool
}

// reconcilePreview ensures the preview reflects the currently selected command,
// starting or cancelling the live log reader as needed. It returns a command to
// open a new reader, or nil when nothing changed.
func (m *Model) reconcilePreview() tea.Cmd {
	c, ok := m.commands.selectedCommand()
	if !ok {
		m.stopPreview()
		m.commands.preview = previewState{status: previewEmpty}
		return nil
	}
	if m.commands.preview.cmdID == c.id {
		return nil // already showing/loading this command; leave its reader alone
	}
	m.stopPreview()
	if c.logDriver == logdriver.DriverNone {
		m.commands.preview = previewState{cmdID: c.id, status: previewNoStorage}
		return nil
	}
	m.commands.preview = previewState{cmdID: c.id, status: previewLoading}
	return m.openPreviewCmd(c.id)
}

// stopPreview cancels the active follow reader, if any.
func (m *Model) stopPreview() {
	if m.commands.preview.stream != nil {
		_ = m.commands.preview.stream.Close()
		m.commands.preview.stream = nil
	}
}

// previewTail sizes the snapshot to ~2x the preview viewport so small scrolls
// and resizes do not immediately re-read logs.
func (m Model) previewTail() int {
	viewport := m.height - 5
	if viewport < 1 {
		viewport = 20
	}
	return max(viewport*2, 50)
}

func (m Model) openPreviewCmd(id string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	tail := m.previewTail()
	return func() tea.Msg {
		stream, err := backend.Logs(ctx, id, tail)
		return previewOpenedMsg{cmdID: id, stream: stream, err: err}
	}
}

func readLineCmd(stream LogStream, id string) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-stream.Lines()
		if !ok {
			return previewLineMsg{cmdID: id, closed: true}
		}
		return previewLineMsg{cmdID: id, line: line.Text, err: line.Err}
	}
}

func (m Model) onPreviewOpened(msg previewOpenedMsg) (tea.Model, tea.Cmd) {
	if msg.cmdID != m.commands.preview.cmdID {
		// Selection moved on; discard this stale reader.
		if msg.stream != nil {
			_ = msg.stream.Close()
		}
		return m, nil
	}
	if msg.err != nil {
		m.commands.preview.status = previewError
		m.commands.preview.errMsg = msg.err.Error()
		return m, nil
	}
	m.commands.preview.stream = msg.stream
	return m, readLineCmd(msg.stream, msg.cmdID)
}

func (m Model) onPreviewLine(msg previewLineMsg) (tea.Model, tea.Cmd) {
	if msg.cmdID != m.commands.preview.cmdID {
		return m, nil // stale reader for a previously selected command
	}
	if msg.closed {
		return m, nil // reader finished; stop pulling
	}
	if msg.err != nil {
		m.commands.preview.status = previewError
		m.commands.preview.errMsg = msg.err.Error()
		return m, nil
	}
	p := &m.commands.preview
	if p.status != previewOK {
		p.status = previewOK
		p.lines = nil
	}
	p.lines = append(p.lines, msg.line)
	if len(p.lines) > previewMaxLines {
		p.lines = p.lines[len(p.lines)-previewMaxLines:]
	}
	return m, readLineCmd(p.stream, msg.cmdID)
}

// --- attach handoff ---------------------------------------------------------

type attachDoneMsg struct {
	name    string
	outcome string
	err     error
}

// attachExec adapts an in-process attach call to tea.ExecCommand so bubbletea
// releases the terminal for the duration of the attach. The std streams set by
// bubbletea are ignored: cli.Attach operates on the real os.Stdin/os.Stdout
// descriptors via the backend.
type attachExec struct {
	run func() error
}

func (a attachExec) Run() error          { return a.run() }
func (a attachExec) SetStdin(io.Reader)  {}
func (a attachExec) SetStdout(io.Writer) {}
func (a attachExec) SetStderr(io.Writer) {}

// startAttach builds the tea.Exec command that hands the terminal to the attach
// session and reports the result back as an attachDoneMsg.
func (m Model) startAttach(id, name string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	var outcome string
	exec := attachExec{run: func() error {
		o, err := backend.Attach(ctx, id)
		outcome = o
		return err
	}}
	return tea.Exec(exec, func(err error) tea.Msg {
		return attachDoneMsg{name: name, outcome: outcome, err: err}
	})
}

func (m Model) onAttachDone(msg attachDoneMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		m.status = fmt.Sprintf("attach %s: %v", msg.name, msg.err)
	case msg.outcome == AttachExited:
		m.status = fmt.Sprintf("%s exited", msg.name)
	default:
		m.status = fmt.Sprintf("detached from %s", msg.name)
	}
	// Redraw cleanly, re-query terminal size, and refresh after the handoff.
	return m, tea.Batch(tea.ClearScreen, tea.WindowSize(), m.loadCommandsCmd())
}

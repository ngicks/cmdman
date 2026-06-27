package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// debounceInterval coalesces a burst of lifecycle events into a single re-list.
const debounceInterval = 150 * time.Millisecond

// previewMaxLines caps the in-memory preview buffer.
const previewMaxLines = 5000

// rawRefreshInterval throttles terminal-view repaints. Raw bytes are drained
// into the emulator off the update loop; the frame is repainted on this fixed
// cadence instead of once per chunk, so a high-rate live program cannot pin the
// Update/View loop (which starves keypresses and tears frames).
const rawRefreshInterval = 80 * time.Millisecond

// Default terminal-view emulator size used until the command's real PTY size
// arrives as the first attach resize report.
const (
	defaultPreviewCols = 80
	defaultPreviewRows = 24
)

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
	if m.active != TabCommands {
		return nil // preview is a Commands-tab concern; leave it as-is
	}
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
	// Terminal-view mode: a running, TTY-backed command has a faithful raw PTY
	// stream, so render it through a vt emulator. Everything else (exited, or a
	// non-TTY log-only command) falls back to the sanitized log text below. A
	// command whose vt preview previously panicked also falls back here, since the
	// predicate includes && !m.termPreviewDisabled.
	if c.state == model.EventTypeRunning && c.tty && !m.termPreviewDisabled {
		m.commands.preview = previewState{cmdID: c.id, status: previewLoading, terminal: true}
		return m.openRawCmd(c.id)
	}
	if c.logDriver == logdriver.DriverNone {
		m.commands.preview = previewState{cmdID: c.id, status: previewNoStorage}
		return nil
	}
	m.commands.preview = previewState{cmdID: c.id, status: previewLoading}
	return m.openPreviewCmd(c.id)
}

// stopPreview cancels the active follow reader and/or raw terminal stream, if
// any, and drops the emulator. The caller reassigns preview afterward.
func (m *Model) stopPreview() {
	if m.commands.preview.stream != nil {
		_ = m.commands.preview.stream.Close()
		m.commands.preview.stream = nil
	}
	if m.commands.preview.raw != nil {
		// Close off the update loop: a raw stream wraps a grpc attach session, and
		// conn.Close() can block. Closing unblocks the drain goroutine (its Recv
		// errors / Chunks() closes); the orphaned emulator is then GC'd.
		closeRawAsync(m.commands.preview.raw)
		m.commands.preview.raw = nil
	}
	m.commands.preview.term = nil
	m.commands.preview.streaming = false
}

// closeRawAsync releases a raw attach stream off the update loop so a blocking
// grpc conn.Close() can never stall bubbletea's Update/View goroutine. Each
// opened stream is closed exactly once (the underlying Close is idempotent).
func closeRawAsync(s RawStream) {
	go func(s RawStream) { _ = s.Close() }(s)
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
	// A duplicate open for the same id (selection bounce A→B→A) can leave an
	// earlier reader in flight; close it before overwriting so its pump
	// goroutine and log reader are released.
	if m.commands.preview.stream != nil {
		_ = m.commands.preview.stream.Close()
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
	p.lines = append(p.lines, sanitizePreviewLine(msg.line))
	if len(p.lines) > previewMaxLines {
		p.lines = p.lines[len(p.lines)-previewMaxLines:]
	}
	return m, readLineCmd(p.stream, msg.cmdID)
}

// --- terminal-view preview (vt emulator over a raw attach stream) -----------

type rawOpenedMsg struct {
	cmdID  string
	stream RawStream
	err    error
}

// rawTickMsg drives the throttled terminal-view repaint cadence. It carries the
// (cmdID, gen) of the loop that scheduled it so a stale tick from a superseded
// preview (fast A→B→A navigation) is ignored rather than spawning a duplicate
// repaint loop.
type rawTickMsg struct {
	cmdID string
	gen   int
}

// rawClosedMsg reports that the background drain finished: the raw stream's
// channel closed (command exited / detached) or yielded an error. It carries
// (cmdID, gen) so a close for a superseded preview is ignored.
type rawClosedMsg struct {
	cmdID    string
	gen      int
	err      error
	panicked bool // the vt emulator panicked draining this stream; fall back to logs
}

func (m Model) openRawCmd(id string) tea.Cmd {
	backend := m.backend
	ctx := m.bgCtx()
	return func() tea.Msg {
		stream, err := backend.RawView(ctx, id)
		return rawOpenedMsg{cmdID: id, stream: stream, err: err}
	}
}

// drainRawCmd pumps the whole raw stream into the shared emulator off the update
// loop: bytes never touch the bubbletea message loop. It writes each chunk
// directly into term (a SafeEmulator, so a concurrent Render/Resize is safe) and
// returns a single rawClosedMsg when the stream channel closes or errors, or a
// rawClosedMsg{panicked: true} if the emulator panics while draining (so the
// model disables terminal-view and falls back to logs).
func drainRawCmd(term *vt.SafeEmulator, stream RawStream, id string, gen int) tea.Cmd {
	return func() (msg tea.Msg) {
		// A full-screen program emits terminal queries (device attributes, cursor
		// reports, mode queries); the vt emulator answers them by writing into its
		// internal, UNBUFFERED response pipe. This preview never sends input back to
		// the remote PTY (D9), so those replies must be drained and discarded — if
		// they are not, the first reply blocks term.Write under the emulator's write
		// lock and starves Render(), hanging the whole UI. Closing the response-pipe
		// writer when draining ends unblocks (and ends) this reader.
		go func() { _, _ = io.Copy(io.Discard, term) }()
		// Unblock (and end) the discard goroutine by closing only the emulator's
		// internal response-pipe writer. We deliberately do NOT call term.Close():
		// that also writes the emulator's unsynchronized `closed` flag, which would
		// data-race the discard goroutine's Read. Closing the pipe writer makes that
		// Read return EOF without touching any shared emulator field.
		defer func() {
			if pw, ok := term.InputPipe().(*io.PipeWriter); ok {
				_ = pw.Close()
			}
		}()
		// The vt/ultraviolet emulator can panic on some real control sequences (a
		// scroll region + XTWINOPS resize + line insert, as a full-screen TUI such
		// as codex emits). A passive preview must never crash the TUI: recover and
		// report a panicked close, so the model disables terminal-view and falls
		// back to the sanitized log preview.
		defer func() {
			if r := recover(); r != nil {
				msg = rawClosedMsg{cmdID: id, gen: gen, panicked: true}
			}
		}()
		for chunk := range stream.Chunks() {
			if chunk.Err != nil {
				return rawClosedMsg{cmdID: id, gen: gen, err: chunk.Err}
			}
			if chunk.Resize != nil {
				if chunk.Resize.Cols > 0 && chunk.Resize.Rows > 0 {
					term.Resize(chunk.Resize.Cols, chunk.Resize.Rows)
				}
				continue
			}
			if len(chunk.Bytes) > 0 {
				_, _ = term.Write(chunk.Bytes)
			}
		}
		return rawClosedMsg{cmdID: id, gen: gen}
	}
}

// rawTickCmd schedules the next throttled repaint for the (cmdID, gen) loop.
func rawTickCmd(id string, gen int) tea.Cmd {
	return tea.Tick(rawRefreshInterval, func(time.Time) tea.Msg {
		return rawTickMsg{cmdID: id, gen: gen}
	})
}

func (m Model) onRawOpened(msg rawOpenedMsg) (tea.Model, tea.Cmd) {
	if msg.cmdID != m.commands.preview.cmdID {
		// Selection moved on; discard this stale stream off the update loop.
		if msg.stream != nil {
			closeRawAsync(msg.stream)
		}
		return m, nil
	}
	if msg.err != nil {
		m.commands.preview.status = previewError
		m.commands.preview.errMsg = msg.err.Error()
		return m, nil
	}
	p := &m.commands.preview
	// A duplicate open for the same id (selection bounce A→B→A) can leave an
	// earlier stream in flight; close it (off the update loop) before overwriting
	// so its drain goroutine and attach session are released.
	if p.raw != nil {
		closeRawAsync(p.raw)
	}
	// Create the emulator at a default size; the command's real PTY size arrives
	// as the first resize chunk over the raw stream and resizes it (D9: the
	// remote PTY is never touched).
	term := vt.NewSafeEmulator(defaultPreviewCols, defaultPreviewRows)
	p.term = term
	p.raw = msg.stream
	p.status = previewOK
	p.streaming = true
	// Bump the monotonic generation so a tick/close from any superseded loop is
	// recognised as stale and dropped.
	m.previewGen++
	p.gen = m.previewGen
	// Start the background drain and the first repaint tick together: the drain
	// feeds the emulator continuously while the tick repaints it on a fixed
	// cadence, decoupling render frequency from the byte rate.
	return m, tea.Batch(
		drainRawCmd(term, msg.stream, msg.cmdID, p.gen),
		rawTickCmd(msg.cmdID, p.gen),
	)
}

func (m Model) onRawTick(msg rawTickMsg) (tea.Model, tea.Cmd) {
	p := m.commands.preview
	if msg.cmdID != p.cmdID || msg.gen != p.gen || !p.streaming {
		return m, nil // a newer preview took over, or the stream closed; stop ticking
	}
	// Probe the render under recover on the Update goroutine: a Render-only panic
	// (one not preceded by a Write panic) cannot be handled in View (value
	// receiver), so catch it here and fall back to the crash-proof log view, the
	// same way the panicked drain does.
	if p.term != nil && renderPanics(p.term) {
		m.termPreviewDisabled = true
		m.stopPreview()
		m.commands.preview = previewState{}
		return m, (&m).reconcilePreview()
	}
	// The repaint is otherwise implicit: bubbletea renders after this Update and
	// renderPreview reads term.Render(). Keep the cadence for this preview.
	return m, rawTickCmd(msg.cmdID, msg.gen)
}

// renderPanics reports whether rendering the emulator panics, recovering so a
// vt/ultraviolet crash never propagates. Used to detect a Render-side panic on
// the Update goroutine so terminal-view can be disabled.
func renderPanics(term *vt.SafeEmulator) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	_ = term.Render()
	return false
}

func (m Model) onRawClosed(msg rawClosedMsg) (tea.Model, tea.Cmd) {
	if msg.cmdID != m.commands.preview.cmdID || msg.gen != m.commands.preview.gen {
		return m, nil // stale stream for a superseded preview
	}
	if msg.panicked {
		// The vt emulator crashed on this command's output. Disable terminal-view
		// for the rest of the session and fall back to the crash-proof sanitized
		// log preview, re-reconciling to open the log reader for the selection.
		m.termPreviewDisabled = true
		m.stopPreview()
		m.commands.preview = previewState{}
		return m, (&m).reconcilePreview()
	}
	if msg.err != nil {
		m.commands.preview.status = previewError
		m.commands.preview.errMsg = msg.err.Error()
		return m, nil
	}
	// Command exited / stream ended: stop the repaint tick and keep the last
	// rendered frame. The raw stream stays set so stopPreview releases the attach
	// session when the selection finally moves off this command.
	m.commands.preview.streaming = false
	return m, nil
}

// renderPreviewTerm renders the current emulator frame as preview lines. Each
// line is defensively terminated with an SGR reset so a style left open at the
// emulator's truncation boundary cannot bleed past the box's right border.
func (m Model) renderPreviewTerm() (lines []string) {
	if m.commands.preview.term == nil {
		return nil
	}
	// term.Render reads the emulator buffer, which a prior write may have left in a
	// state the vt/ultraviolet library mishandles; never let that crash the TUI.
	defer func() {
		if r := recover(); r != nil {
			lines = []string{"(terminal preview unavailable)"}
		}
	}()
	rendered := strings.Split(m.commands.preview.term.Render(), "\n")
	for i := range rendered {
		rendered[i] += ansi.ResetStyle
	}
	return rendered
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
	return m, tea.Batch(tea.ClearScreen, tea.RequestWindowSize, m.loadCommandsCmd())
}

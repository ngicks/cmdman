package cmdman

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// screenTracker maintains a server-side terminal emulator fed with a TTY
// command's full raw output, so a (re)attaching client can be handed a coherent
// snapshot of the CURRENT screen instead of raw scrollback bytes.
//
// The raw byte scrollback is a fixed-size ring buffer (1 MiB by default). For a
// full-screen or incrementally-updating TTY program it rotates, so its oldest
// bytes — the alternate-screen enter, an initial full paint, static chrome —
// scroll out. Replaying that truncated stream into a fresh client emulator
// reconstructs a garbled partial frame: the reported "preview breaks when
// transitioning among commands" bug, since every selection change re-attaches
// into a fresh emulator. A persistent emulator here never loses screen state to
// ring rotation, so its snapshot always reconstructs the exact current screen.
//
// The vt/ultraviolet emulator has two known hazards this type contains so they
// can never reach the monitor's critical output path:
//   - It answers terminal queries (DA1/DA2, cursor reports) by writing into an
//     unbuffered response pipe; undrained, the first reply blocks Write forever
//     (the D12 popup hang). A drain goroutine empties the pipe for its lifetime.
//   - It can panic on some real control-sequence combinations (the D13 codex
//     crash). Every emulator call recovers: a panic marks the tracker unhealthy
//     and the monitor falls back to raw scrollback replay for that command.
type screenTracker struct {
	term    *vt.Emulator
	healthy bool
}

// newScreenTracker creates a tracker sized to the command's PTY and starts the
// response-pipe drain that keeps feeds from deadlocking (see the type doc).
func newScreenTracker(cols, rows int) *screenTracker {
	if cols <= 0 || rows <= 0 {
		cols, rows = int(defaultPtyCols), int(defaultPtyRows)
	}
	t := &screenTracker{term: vt.NewEmulator(cols, rows), healthy: true}
	// Drain the emulator's unbuffered response pipe so a query reply can never
	// block term.Write under the monitor's outputMu (D12). io.Discard never
	// errors; the reader ends only when close() shuts the pipe writer.
	go func() { _, _ = io.Copy(io.Discard, t.term) }()
	return t
}

// feed writes a raw output chunk into the emulator. A vt panic (D13) disables
// the tracker rather than propagating into the monitor's output path.
func (t *screenTracker) feed(data []byte) {
	if t == nil || !t.healthy || len(data) == 0 {
		return
	}
	defer t.recoverDisable()
	_, _ = t.term.Write(data)
}

// resize matches the emulator to a new PTY size so replayed content keeps the
// command's real layout.
func (t *screenTracker) resize(cols, rows int) {
	if t == nil || !t.healthy || cols <= 0 || rows <= 0 {
		return
	}
	defer t.recoverDisable()
	t.term.Resize(cols, rows)
}

// snapshot renders the current screen as a self-contained repaint sequence that
// reconstructs it on a fresh client emulator: clear, then each row painted at an
// absolute position (so a full-width line cannot reflow onto the next), then the
// cursor restored. It returns nil when the tracker is unhealthy so the caller
// falls back to raw scrollback.
func (t *screenTracker) snapshot() (out []byte) {
	if t == nil || !t.healthy {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			t.healthy = false
			out = nil
		}
	}()
	lines := strings.Split(t.term.Render(), "\n")
	var buf bytes.Buffer
	if t.term.IsAltScreen() {
		// Mirror the program's alternate screen so an interactive attach restores
		// the user's screen on the program's alt-screen leave.
		buf.WriteString("\x1b[?1049h")
	}
	buf.WriteString("\x1b[2J") // erase the whole screen before repainting
	for i, line := range lines {
		// Absolute-position each row and reset the pen first so a prior row's
		// trailing style cannot bleed into this one.
		fmt.Fprintf(&buf, "\x1b[%d;1H%s%s", i+1, ansi.ResetStyle, line)
	}
	pos := t.term.CursorPosition()
	fmt.Fprintf(&buf, "\x1b[%d;%dH%s", pos.Y+1, pos.X+1, ansi.ResetStyle)
	return buf.Bytes()
}

// close ends the drain goroutine by closing the emulator's response-pipe writer.
// It deliberately does not call term.Close(), which would race the drain's Read
// on the emulator's unsynchronized closed flag; closing the pipe writer makes
// that Read return EOF without touching shared emulator state.
func (t *screenTracker) close() {
	if t == nil {
		return
	}
	if pw, ok := t.term.InputPipe().(*io.PipeWriter); ok {
		_ = pw.Close()
	}
}

func (t *screenTracker) recoverDisable() {
	if r := recover(); r != nil {
		t.healthy = false
	}
}

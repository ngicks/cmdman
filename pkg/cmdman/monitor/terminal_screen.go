package monitor

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// screenTracker preserves a TTY command's current screen after raw scrollback
// rotates. It drains emulator replies to avoid blocking terminal queries and
// recovers emulator panics, falling back to raw scrollback.
type screenTracker struct {
	term    *vt.Emulator
	healthy bool
}

// newScreenTracker uses cols and rows as PTY dimensions. If either is
// nonpositive, both reset to their defaults. It drains terminal-query responses.
// The emulator doubles as the capture point for the run's runtime state: st
// registers its latch hooks on it and must not be nil.
func newScreenTracker(cols, rows int, st *commandRuntimeState) *screenTracker {
	if cols <= 0 || rows <= 0 {
		cols, rows = int(defaultPtyCols), int(defaultPtyRows)
	}
	t := &screenTracker{term: vt.NewEmulator(cols, rows), healthy: true}
	st.observe(t.term)
	// Terminal-query replies use an unbuffered pipe and must be drained.
	go func() { _, _ = io.Copy(io.Discard, t.term) }()
	return t
}

// feed applies a raw command-output chunk. A nil or unhealthy receiver and
// empty data are ignored; an emulator panic marks the tracker unhealthy.
func (t *screenTracker) feed(data []byte) {
	if t == nil || !t.healthy || len(data) == 0 {
		return
	}
	defer t.recoverDisable()
	_, _ = t.term.Write(data)
}

// resize applies new PTY dimensions. A nil or unhealthy receiver and
// nonpositive dimensions are ignored; an emulator panic marks the tracker unhealthy.
func (t *screenTracker) resize(cols, rows int) {
	if t == nil || !t.healthy || cols <= 0 || rows <= 0 {
		return
	}
	defer t.recoverDisable()
	t.term.Resize(cols, rows)
}

// snapshot returns a self-contained repaint. A nil or unhealthy receiver
// returns nil; an emulator panic marks the tracker unhealthy and returns nil.
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
		// Preserve alternate-screen leave semantics for interactive clients.
		buf.WriteString("\x1b[?1049h")
	}
	buf.WriteString("\x1b[2J") // erase the whole screen before repainting
	for i, line := range lines {
		// Absolute positions prevent full-width rows from reflowing.
		fmt.Fprintf(&buf, "\x1b[%d;1H%s%s", i+1, ansi.ResetStyle, line)
	}
	pos := t.term.CursorPosition()
	fmt.Fprintf(&buf, "\x1b[%d;%dH%s", pos.Y+1, pos.X+1, ansi.ResetStyle)
	return buf.Bytes()
}

// close closes the response pipe so the drain goroutine can exit. A nil
// receiver is a no-op; term.Close is avoided because its closed flag races.
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

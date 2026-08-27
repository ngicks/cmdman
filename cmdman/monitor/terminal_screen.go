package monitor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	ansi "github.com/ngicks/cmdman/internal/third_party/charmbracelet-x-ansi"
	vt "github.com/ngicks/cmdman/internal/third_party/charmbracelet-x-vt"
)

var (
	errScreenUnavailable = errors.New("terminal screen unavailable")
	errNoAltScreen       = errors.New("no alternate screen")
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

// captureOptions selects what capture renders. Each field mirrors a flag of
// tmux's capture-pane: escapes is -e, altScreen -a, quiet -q,
// preserveTrailingSpaces -N, and the remaining fields the -S/-E line range.
// Range values share one index space: 0 is the topmost visible row, rows-1 the
// bottommost, and -1 the newest history line.
type captureOptions struct {
	escapes                bool
	altScreen              bool
	quiet                  bool
	preserveTrailingSpaces bool
	start                  int
	startSet               bool
	startWholeHistory      bool
	end                    int
	endSet                 bool
	endWholeScreen         bool
}

// capture renders bare lines joined by "\n", unlike snapshot's repaint: no
// erase, no cursor addressing, so any sub-range pastes as-is. Callers must
// already hold the monitor's output mutex; capture locks nothing itself.
//
// A nil or unhealthy receiver returns errScreenUnavailable, and an emulator
// panic marks the tracker unhealthy and returns the same. Asking for the
// alternate screen while it is inactive returns errNoAltScreen unless quiet is
// set, in which case the capture is empty. While the alternate screen is
// active but not requested, the visible rows are the alternate screen's while
// history stays the main screen's, which is what tmux reports as well.
func (t *screenTracker) capture(opts captureOptions) (out []byte, err error) {
	if t == nil || !t.healthy {
		return nil, fmt.Errorf("capture: %w", errScreenUnavailable)
	}
	defer func() {
		if r := recover(); r != nil {
			t.healthy = false
			out = nil
			err = fmt.Errorf("capture: %w: emulator panicked: %v", errScreenUnavailable, r)
		}
	}()

	if opts.altScreen && !t.term.IsAltScreen() {
		if opts.quiet {
			return nil, nil
		}
		return nil, fmt.Errorf("capture: %w", errNoAltScreen)
	}

	width, rows := t.term.Width(), t.term.Height()
	history := t.term.Scrollback()
	histLen := 0
	if !opts.altScreen {
		histLen = history.Len()
	}

	start, end := captureRange(opts, histLen, rows)
	if end < start {
		return nil, nil
	}

	var buf bytes.Buffer
	for i := start; i <= end; i++ {
		var line uv.Line
		if i < 0 {
			line = history.Line(histLen + i)
		} else {
			line = t.visibleLine(i, width)
		}
		buf.WriteString(captureLine(line, width, opts))
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// captureRange clamps the requested range to the lines that exist, as tmux
// does, rather than rejecting it.
func captureRange(opts captureOptions, histLen, rows int) (start, end int) {
	first, last := -histLen, rows-1
	clamp := func(v int) int { return min(max(v, first), last) }

	start = 0
	switch {
	case opts.startWholeHistory:
		start = first
	case opts.startSet:
		start = clamp(opts.start)
	}
	end = last
	if opts.endSet && !opts.endWholeScreen {
		end = clamp(opts.end)
	}
	return start, end
}

func (t *screenTracker) visibleLine(y, width int) uv.Line {
	line := make(uv.Line, width)
	for x := range width {
		line[x] = uv.EmptyCell
		if c := t.term.CellAt(x, y); c != nil {
			line[x] = *c
		}
	}
	return line
}

// captureLine renders one row. Zero cells hold the tail of a wide rune and
// emit nothing of their own; the rune's own cell already spans them.
func captureLine(line uv.Line, width int, opts captureOptions) string {
	n := len(line)
	if opts.preserveTrailingSpaces {
		// Scrollback drops trailing blanks on push, so short history lines are
		// padded back out to keep every captured row the same width.
		n = max(n, width)
	} else {
		for n > 0 && isBlankCell(&line[n-1]) {
			n--
		}
	}
	if n == 0 {
		return ""
	}

	blank := uv.EmptyCell
	var b strings.Builder
	var pen uv.Style
	var link uv.Link
	if opts.escapes {
		b.WriteString(ansi.ResetStyle)
	}
	for x := range n {
		c := &blank
		if x < len(line) {
			c = &line[x]
		}
		if c.IsZero() {
			continue
		}
		if opts.escapes {
			if !c.Style.Equal(&pen) {
				b.WriteString(c.Style.Diff(&pen))
				pen = c.Style
			}
			if c.Link != link {
				if link.URL != "" {
					b.WriteString(ansi.ResetHyperlink())
				}
				if c.Link.URL != "" {
					b.WriteString(ansi.SetHyperlink(c.Link.URL, c.Link.Params))
				}
				link = c.Link
			}
		}
		if c.Content == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Content)
		}
	}
	if opts.escapes {
		if link.URL != "" {
			b.WriteString(ansi.ResetHyperlink())
		}
		b.WriteString(ansi.ResetStyle)
	}
	return b.String()
}

func isBlankCell(c *uv.Cell) bool {
	return c.IsZero() || c.Equal(&uv.EmptyCell)
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

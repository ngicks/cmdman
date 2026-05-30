package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

// ttyReporter renders a live, inline (no alt-screen) state trace: one line per
// command, repainted in place as events arrive. It deliberately avoids a TUI
// framework — a full framework (bubbletea) queries the terminal at process
// startup for the whole binary, which corrupts the PTY of sibling subcommands
// such as `compose attach`. This renderer only touches the terminal while it is
// actually running.
//
// Events arrive from the reconcile walk's goroutines via Report; a background
// ticker animates the spinner on in-progress lines. Both paths repaint under the
// same mutex. Close stops the ticker, draws the final frame, and leaves it in
// the scrollback.
type ttyReporter struct {
	mu       sync.Mutex
	out      io.Writer
	order    []string
	byName   map[string]progressEntry
	frame    int
	drawn    int // command lines currently on screen
	closed   bool
	stopTick chan struct{}
	tickDone chan struct{}
}

func newTTYReporter(out io.Writer) *ttyReporter {
	r := &ttyReporter{
		out:      out,
		byName:   map[string]progressEntry{},
		stopTick: make(chan struct{}),
		tickDone: make(chan struct{}),
	}
	go r.animate()
	return r
}

func (r *ttyReporter) Report(ev compose.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if _, seen := r.byName[ev.Command]; !seen {
		r.order = append(r.order, ev.Command)
	}
	r.byName[ev.Command] = progressEntry{
		phase: ev.Phase,
		err:   errString(ev.Err),
		exit:  ev.ExitCode,
	}
	r.render()
}

func (r *ttyReporter) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.render() // final frame
	r.mu.Unlock()

	close(r.stopTick)
	<-r.tickDone
	return nil
}

// animate advances the spinner while any command is still in progress.
func (r *ttyReporter) animate() {
	defer close(r.tickDone)
	t := time.NewTicker(spinnerInterval)
	defer t.Stop()
	for {
		select {
		case <-r.stopTick:
			return
		case <-t.C:
			r.mu.Lock()
			if !r.closed && r.hasInProgress() {
				r.frame++
				r.render()
			}
			r.mu.Unlock()
		}
	}
}

// render repaints the whole block in place. The caller holds r.mu. It moves the
// cursor up over the previously drawn lines, then clears and rewrites each line,
// so a repaint costs no scrollback. order only grows, so the up-count always
// matches what is on screen.
func (r *ttyReporter) render() {
	var b strings.Builder
	if r.drawn > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", r.drawn) // cursor up to the top of the block
	}
	for _, name := range r.order {
		b.WriteString("\r\x1b[2K") // carriage return + clear entire line
		b.WriteString(renderProgressLine(name, r.byName[name], r.frame))
		b.WriteByte('\n')
	}
	r.drawn = len(r.order)
	_, _ = io.WriteString(r.out, b.String())
}

func (r *ttyReporter) hasInProgress() bool {
	for _, e := range r.byName {
		if !e.phase.Terminal() {
			return true
		}
	}
	return false
}

// progressEntry is the latest known state of one command.
type progressEntry struct {
	phase compose.Phase
	err   string
	exit  *int
}

// spinnerFrames is the braille spinner used for in-progress phases.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond

var (
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // in progress
	stylePending = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // created/unchanged
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // running/completed
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // error/failed
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // skipped
)

// renderProgressLine renders one command's status line: a phase marker, the
// command name, the phase label, and (when present) the exit code and error.
func renderProgressLine(name string, e progressEntry, frame int) string {
	var b strings.Builder
	b.WriteString(progressMarker(e.phase, frame))
	b.WriteByte(' ')
	fmt.Fprintf(&b, "%-16s %s", name, phaseLabel(e.phase))
	if e.exit != nil {
		fmt.Fprintf(&b, " (exit %d)", *e.exit)
	}
	if e.err != "" {
		b.WriteString(styleErr.Render("  " + firstLine(e.err)))
	}
	return b.String()
}

// progressMarker selects the leading glyph for a phase by status category:
//
//	in progress  ⠹  spinner (dim)        creating/recreating/starting/waiting/stopping/removing
//	pending      ◌  cyan                 created/recreated/unchanged (ready, not running)
//	running      ●  green                started
//	completed    ✔  green                exited/stopped/removed
//	skipped      ⊘  yellow               skipped
//	failed       ✘  red                  error/failed
func progressMarker(p compose.Phase, frame int) string {
	switch {
	case !p.Terminal():
		return styleDim.Render(spinnerFrames[frame%len(spinnerFrames)])
	case p.Failed():
		return styleErr.Render("✘")
	case p == compose.PhaseSkipped:
		return styleWarn.Render("⊘")
	case isPendingPhase(p):
		return stylePending.Render("◌")
	case p == compose.PhaseStarted:
		return styleOK.Render("●")
	default: // completed: exited, stopped, removed
		return styleOK.Render("✔")
	}
}

// isPendingPhase reports whether p is a terminal create-phase result that leaves
// the command ready but not yet running.
func isPendingPhase(p compose.Phase) bool {
	switch p {
	case compose.PhaseCreated, compose.PhaseRecreated, compose.PhaseUnchanged:
		return true
	default:
		return false
	}
}

// firstLine returns s up to its first newline, so a multi-line error stays on
// one display row.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

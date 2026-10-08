package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// ttyReporter renders a live, inline (no alt-screen) state trace, repainted in
// place as events arrive. It deliberately avoids a TUI framework — a full
// framework (bubbletea) queries the terminal at process startup for the whole
// binary, which corrupts the PTY of sibling subcommands such as `compose
// attach`. This renderer only touches the terminal while it is actually running.
//
// A command moves through one or more lifecycle steps (stop, recreate, start,
// remove, …). Each step gets its own line: while the step is in flight its
// transient phase (e.g. "starting") owns the line and collapses onto it when the
// step settles (e.g. "running"), but a settled milestone is never overwritten by
// the next step — so a recreated-then-started command keeps "recreated",
// "stopped", and "running" each visible rather than only its latest phase.
//
// Events arrive from the reconcile walk's goroutines via Report; a background
// ticker animates the spinner on in-progress lines. Both paths repaint under the
// same mutex. Close stops the ticker, draws the final frame, and leaves it in
// the scrollback.
type ttyReporter struct {
	mu       sync.Mutex
	out      io.Writer
	order    []string                   // command names in first-seen order
	lines    map[string][]progressEntry // per-command lifecycle-step lines
	frame    int
	drawn    int // total lines currently on screen
	closed   bool
	stopTick chan struct{}
	tickDone chan struct{}
}

func newTTYReporter(out io.Writer) *ttyReporter {
	r := &ttyReporter{
		out:      out,
		lines:    map[string][]progressEntry{},
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
	// Each run of a hook event gets a line of its own, so its steps never
	// refine or settle the steps of the replica it runs for. So does each
	// unreleased resource, which a failure on the replica's line would
	// otherwise swallow.
	name := ev.Command
	switch {
	case ev.Hook != "":
		name = fmt.Sprintf("%s hook %s.%s", ev.Command, ev.Hook, ev.Lifecycle)
	case ev.Phase == compose.PhaseUnreleased:
		name = fmt.Sprintf("%s resource %s", ev.Command, ev.Resource)
	}
	steps := r.lines[name]
	if ev.Phase == compose.PhaseHookOutput {
		// Output is no step: the latest line shows on the hook's line while the
		// hook runs.
		if n := len(steps); n > 0 && !steps[n-1].phase.Terminal() {
			steps[n-1].output = ev.Line
			r.render()
		}
		return
	}
	entry := progressEntry{
		phase:       ev.Phase,
		err:         errString(ev.Err),
		exit:        ev.ExitCode,
		forceKilled: ev.ForceKilled,
	}
	if ev.Phase == compose.PhaseUnreleased {
		entry.unreleased = unreleasedResource{
			owner: ev.Command,
			key:   ev.Resource,
			value: ev.Value,
		}
	}
	switch {
	case len(steps) == 0:
		r.order = append(r.order, name)
		r.lines[name] = []progressEntry{entry}
	case steps[len(steps)-1].phase.Failed():
		// A failure is sticky: once a step has failed during this operation, keep
		// that failure (its kind and detail) as the command's terminal outcome
		// instead of trailing it with a later success line — e.g. the idempotent
		// "running" the start walk reports for a command whose recreate-stop
		// failed, which would otherwise read as a clean restart and hide the cause.
		return
	case steps[len(steps)-1].phase.Terminal():
		// The previous step reached a terminal milestone; this event opens a new
		// step on its own line, leaving the milestone visible above it.
		r.lines[name] = append(steps, entry)
	default:
		// The current step is still in flight; refine it in place (transient →
		// transient, or transient → terminal collapses onto the same line).
		steps[len(steps)-1] = entry
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
	// render leaves the cursor on the block's last line (no trailing newline);
	// move below it so the shell prompt / later output starts on a fresh line.
	// An operation that reported nothing, such as a restart of commands without
	// hooks, drew no block, and a newline would only leave an empty line.
	if r.drawn > 0 {
		_, _ = io.WriteString(r.out, "\n")
	}
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
// so a repaint costs no scrollback. The line count only grows (steps are
// appended, never removed), so the up-count always matches what is on screen.
//
// Crucially it writes NO trailing newline after the last line, leaving the
// cursor on the block's final line rather than on a fresh line below it. A
// trailing newline at the bottom row of the terminal would scroll the screen on
// every repaint, desyncing the cursor-up count and leaking a copy of the block
// into scrollback each spinner tick. Keeping the cursor inside the block means a
// same-height repaint never emits a newline and so never scrolls. (Growing past
// the terminal height still scrolls, an inherent limit of inline rendering.)
func (r *ttyReporter) render() {
	var b strings.Builder
	// The cursor sits on the last drawn line; step up to the first one.
	if r.drawn > 1 {
		fmt.Fprintf(&b, "\x1b[%dA", r.drawn-1)
	}
	total := 0
	for _, name := range r.order {
		for _, e := range r.lines[name] {
			for _, row := range renderProgressRows(name, e, r.frame) {
				if total > 0 {
					b.WriteByte('\n') // separate lines; none trails the final line
				}
				b.WriteString("\r\x1b[2K") // carriage return + clear entire line
				b.WriteString(row)
				total++
			}
		}
	}
	r.drawn = total
	_, _ = io.WriteString(r.out, b.String())
}

// hasInProgress reports whether any command's current step is still in flight.
// Only the last line of a command can be transient; earlier lines are settled
// milestones.
func (r *ttyReporter) hasInProgress() bool {
	for _, steps := range r.lines {
		if n := len(steps); n > 0 && !steps[n-1].phase.Terminal() {
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
	// output is the latest output line of a running hook.
	output string
	// forceKilled marks a stop that ran out the grace period and ended the
	// command with SIGKILL.
	forceKilled bool
	// unreleased names the resource of a PhaseUnreleased line.
	unreleased unreleasedResource
}

// unreleasedResource is a resource whose release failed: the replica it is
// held for, its key and its value.
type unreleasedResource struct {
	owner, key, value string
}

// maxOutputRunes bounds the hook output shown on a progress line. A line that
// wraps would break the repaint, which counts one terminal row per line.
const maxOutputRunes = 60

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

// renderProgressRows renders the terminal rows of one step: its status line,
// and right below it a warning for a stop that force-killed the command. The
// warning gets a row of its own rather than a place on the status line, where
// it would wrap an 80-column terminal and break the repaint's count of one row
// per line. It leaves the name to the status line above it for the same
// reason.
func renderProgressRows(name string, e progressEntry, frame int) []string {
	if e.phase == compose.PhaseUnreleased {
		return []string{renderUnreleasedLine(e)}
	}
	rows := []string{renderProgressLine(name, e, frame)}
	if e.forceKilled {
		rows = append(rows, "  "+styleWarn.Render("! "+forceKilledNote))
	}
	return rows
}

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
		// An ignored failure is shown for what it was, without alarming color.
		style := styleErr
		if e.phase == compose.PhaseHookIgnored {
			style = styleDim
		}
		b.WriteString(style.Render("  " + firstLine(e.err)))
	}
	if e.output != "" && !e.phase.Terminal() {
		b.WriteString(styleDim.Render("  " + outputSnippet(e.output)))
	}
	return b.String()
}

// renderUnreleasedLine renders the line of a resource whose release failed.
func renderUnreleasedLine(e progressEntry) string {
	u := e.unreleased
	return fmt.Sprintf("%s %s: resource %s (%s) not released: %s",
		progressMarker(e.phase, 0), u.owner, u.key, u.value, firstLine(e.err))
}

// outputSnippet returns s fit for one progress line: control characters, which
// would move the cursor or restyle the line, dropped and the rest cut to
// maxOutputRunes.
func outputSnippet(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if runes := []rune(s); len(runes) > maxOutputRunes {
		return string(runes[:maxOutputRunes-1]) + "…"
	}
	return s
}

// progressMarker selects the leading glyph for a phase by status category:
//
//	in progress  ⠹  spinner (dim)        creating/recreating/starting/waiting/stopping/removing
//	pending      ◌  cyan                 created/recreated/unchanged (ready, not running)
//	running      ●  green                running
//	completed    ✔  green                exited/stopped/removed
//	skipped      ⊘  yellow               skipped
//	warning      !  yellow               hook-warning/unreleased
//	ignored      -  dim                  hook-ignored
//	failed       ✘  red                  error/failed/hook-failed
func progressMarker(p compose.Phase, frame int) string {
	switch {
	case !p.Terminal():
		return styleDim.Render(spinnerFrames[frame%len(spinnerFrames)])
	case p.Failed():
		return styleErr.Render("✘")
	case p == compose.PhaseSkipped:
		return styleWarn.Render("⊘")
	case p == compose.PhaseHookWarning, p == compose.PhaseUnreleased:
		return styleWarn.Render("!")
	case p == compose.PhaseHookIgnored:
		return styleDim.Render("-")
	case isPendingPhase(p):
		return stylePending.Render("◌")
	case p == compose.PhaseRunning:
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

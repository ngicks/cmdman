package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// ProgressMode selects how the progress of a compose lifecycle operation
// (create, up, start, stop, restart, down or scale) is rendered.
type ProgressMode string

const (
	// ProgressAuto renders a live terminal display when stdout is a terminal and
	// emits JSONL otherwise. It is the default.
	ProgressAuto ProgressMode = "auto"
	// ProgressTTY forces the live terminal display regardless of stdout.
	ProgressTTY ProgressMode = "tty"
	// ProgressJSON forces JSONL output regardless of stdout.
	ProgressJSON ProgressMode = "json"
	// ProgressQuiet suppresses progress output entirely.
	ProgressQuiet ProgressMode = "quiet"
)

// ParseProgressMode validates and normalizes a --progress flag value.
func ParseProgressMode(s string) (ProgressMode, error) {
	switch ProgressMode(s) {
	case ProgressAuto, "":
		return ProgressAuto, nil
	case ProgressTTY:
		return ProgressTTY, nil
	case ProgressJSON:
		return ProgressJSON, nil
	case ProgressQuiet:
		return ProgressQuiet, nil
	default:
		return "", fmt.Errorf(
			"invalid --progress %q: want one of auto, tty, json, quiet", s)
	}
}

// ProgressFlagUsage is the --progress flag help text.
const ProgressFlagUsage = "Progress output: auto (tty if terminal, else json), tty, json, or quiet"

// ComposeProgress is a compose.Reporter whose rendering is finalized by Close.
// Close must be called exactly once, after the operation returns.
type ComposeProgress interface {
	compose.Reporter
	// Close flushes and finalizes the display (e.g. leaves the final terminal
	// frame in place, or stops the render loop). Safe to call once.
	Close() error
}

// NewComposeProgress builds a progress reporter for op, the compose verb it
// reports ("up", "create", "restart", ...), choosing the renderer from mode
// and, for ProgressAuto, whether out is a terminal.
func NewComposeProgress(out io.Writer, mode ProgressMode, op string) ComposeProgress {
	switch resolveProgressMode(out, mode) {
	case ProgressQuiet:
		return quietReporter{}
	case ProgressTTY:
		return newTTYReporter(out)
	default: // ProgressJSON
		return newJSONReporter(out, op)
	}
}

// resolveProgressMode collapses ProgressAuto to a concrete mode based on whether
// out is a terminal. Explicit modes pass through unchanged.
func resolveProgressMode(out io.Writer, mode ProgressMode) ProgressMode {
	if mode == ProgressAuto || mode == "" {
		if isTerminalWriter(out) {
			return ProgressTTY
		}
		return ProgressJSON
	}
	return mode
}

// isTerminalWriter reports whether out is a terminal-backed *os.File.
func isTerminalWriter(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// quietReporter discards every event.
type quietReporter struct{}

func (quietReporter) Report(compose.Event) {}
func (quietReporter) Close() error         { return nil }

// progressLine is the JSONL wire shape emitted by jsonReporter. One object per
// line, reporting a command's state transition and, on a terminal phase, its
// result (exit code / error). A hook event also names the hook run it belongs
// to, and a hook-output event carries one line of the hook's output. A stopped
// event says whether the stop ran out the grace period and ended the command
// with SIGKILL.
type progressLine struct {
	Op          string `json:"op"`
	Command     string `json:"command"`
	Phase       string `json:"phase"`
	Terminal    bool   `json:"terminal"`
	ExitCode    *int   `json:"exitCode,omitzero"`
	Error       string `json:"error,omitzero"`
	ForceKilled bool   `json:"forceKilled,omitzero"`
	ScaleIndex  int    `json:"scaleIndex,omitzero"`
	Hook        string `json:"hook,omitzero"`
	Lifecycle   string `json:"lifecycle,omitzero"`
	Exec        string `json:"exec,omitzero"`
	Stream      string `json:"stream,omitzero"`
	Line        string `json:"line,omitzero"`
}

// jsonReporter writes one JSON object per event, newline-delimited (JSONL).
type jsonReporter struct {
	mu  sync.Mutex
	enc *json.Encoder
	op  string
}

func newJSONReporter(out io.Writer, op string) *jsonReporter {
	return &jsonReporter{enc: json.NewEncoder(out), op: op}
}

func (r *jsonReporter) Report(ev compose.Event) {
	line := progressLine{
		Op:          r.op,
		Command:     ev.Command,
		Phase:       string(ev.Phase),
		Terminal:    ev.Phase.Terminal(),
		ExitCode:    ev.ExitCode,
		ForceKilled: ev.ForceKilled,
		ScaleIndex:  ev.ScaleIndex,
		Hook:        ev.Hook,
		Lifecycle:   string(ev.Lifecycle),
		Exec:        ev.Exec,
		Stream:      string(ev.Stream),
		Line:        ev.Line,
	}
	if ev.Err != nil {
		line.Error = ev.Err.Error()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// json.Encoder.Encode appends a newline, yielding JSONL.
	_ = r.enc.Encode(line)
}

func (r *jsonReporter) Close() error { return nil }

// errString renders err for display, empty when nil.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// aggregateErrors returns nil when errs is empty, otherwise a single error that
// names how many operations failed. It does not print; the progress reporter has
// already surfaced per-command detail.
func aggregateErrors(noun string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%d %s(s) failed", len(errs), noun)
}

// UpResultErr returns a combined error when any create or start failed during a
// compose up. Display is handled by the progress reporter; this only drives the
// command's exit status.
func UpResultErr(result *compose.UpResult) error {
	var errs []error
	for _, a := range result.Actions {
		if a.Err != nil {
			errs = append(errs, a.Err)
		}
	}
	for _, s := range result.Starts {
		if s.Err != nil {
			errs = append(errs, s.Err)
		}
	}
	return aggregateErrors("compose operation", errs)
}

// StartResultErr returns a combined error when any start failed.
func StartResultErr(starts []compose.StartOutcome) error {
	var errs []error
	for _, s := range starts {
		if s.Err != nil {
			errs = append(errs, s.Err)
		}
	}
	return aggregateErrors("compose start operation", errs)
}

// StopResultErr returns a combined error when any stop failed.
func StopResultErr(stops []compose.StopOutcome) error {
	var errs []error
	for _, s := range stops {
		if s.Err != nil {
			errs = append(errs, s.Err)
		}
	}
	return aggregateErrors("compose stop operation", errs)
}

// DownResultErr returns a combined error when any stop, remove or release
// failed.
func DownResultErr(result *compose.DownResult) error {
	var errs []error
	for _, s := range result.Stops {
		if s.Err != nil {
			errs = append(errs, s.Err)
		}
	}
	for _, r := range result.Removes {
		if r.Err != nil {
			errs = append(errs, r.Err)
		}
	}
	for _, r := range result.Releases {
		if r.Err != nil {
			errs = append(errs, r.Err)
		}
	}
	return aggregateErrors("compose down operation", errs)
}

// phaseLabel renders a Phase as a capitalized human label.
func phaseLabel(p compose.Phase) string {
	s := string(p)
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

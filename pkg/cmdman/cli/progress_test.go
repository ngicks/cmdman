package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func TestParseProgressMode(t *testing.T) {
	cases := map[string]struct {
		want    ProgressMode
		wantErr bool
	}{
		"":      {want: ProgressAuto},
		"auto":  {want: ProgressAuto},
		"tty":   {want: ProgressTTY},
		"json":  {want: ProgressJSON},
		"quiet": {want: ProgressQuiet},
		"bogus": {wantErr: true},
	}
	for in, want := range cases {
		got, err := ParseProgressMode(in)
		if want.wantErr {
			if err == nil {
				t.Errorf("ParseProgressMode(%q): expected error, got %q", in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseProgressMode(%q): unexpected error: %v", in, err)
			continue
		}
		if got != want.want {
			t.Errorf("ParseProgressMode(%q) = %q, want %q", in, got, want.want)
		}
	}
}

func TestResolveProgressModeNonTerminalIsJSON(t *testing.T) {
	// A bytes.Buffer is not a terminal-backed *os.File, so auto resolves to json.
	var buf bytes.Buffer
	if got := resolveProgressMode(&buf, ProgressAuto); got != ProgressJSON {
		t.Fatalf("auto on non-terminal = %q, want json", got)
	}
	// Explicit modes pass through unchanged regardless of the writer.
	for _, m := range []ProgressMode{ProgressTTY, ProgressJSON, ProgressQuiet} {
		if got := resolveProgressMode(&buf, m); got != m {
			t.Fatalf("resolveProgressMode(%q) = %q, want unchanged", m, got)
		}
	}
}

func TestJSONReporterEmitsJSONL(t *testing.T) {
	var buf bytes.Buffer
	r := newJSONReporter(&buf, "up")

	exit := 2
	r.Report(compose.Event{Command: "api", Phase: compose.PhaseStarting})
	r.Report(compose.Event{Command: "api", Phase: compose.PhaseRunning})
	r.Report(compose.Event{Command: "worker", Phase: compose.PhaseExited, ExitCode: &exit})
	r.Report(compose.Event{
		Command: "db",
		Phase:   compose.PhaseError,
		Err:     errors.New("start command \"db\": boom"),
	})
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := splitNonEmptyLines(buf.String())
	if len(lines) != 4 {
		t.Fatalf("expected 4 JSONL lines, got %d:\n%s", len(lines), buf.String())
	}

	var got []progressLine
	for _, l := range lines {
		var pl progressLine
		if err := json.Unmarshal([]byte(l), &pl); err != nil {
			t.Fatalf("invalid JSON line %q: %v", l, err)
		}
		got = append(got, pl)
	}

	// op is stamped on every line.
	for _, pl := range got {
		if pl.Op != "up" {
			t.Errorf("op = %q, want up", pl.Op)
		}
	}
	// Transient vs terminal flag.
	if got[0].Phase != "starting" || got[0].Terminal {
		t.Errorf("starting line wrong: %+v", got[0])
	}
	if got[1].Phase != "running" || !got[1].Terminal {
		t.Errorf("running line wrong: %+v", got[1])
	}
	// Exit code surfaced on terminal exited phase.
	if got[2].Phase != "exited" || got[2].ExitCode == nil || *got[2].ExitCode != 2 {
		t.Errorf("exited line wrong: %+v", got[2])
	}
	// Error string surfaced on error phase.
	if got[3].Phase != "error" || !strings.Contains(got[3].Error, "boom") {
		t.Errorf("error line wrong: %+v", got[3])
	}
	// Non-error lines omit the error field.
	if got[0].Error != "" {
		t.Errorf("starting line should omit error, got %q", got[0].Error)
	}
}

func TestTTYReporterRendersTrace(t *testing.T) {
	var buf bytes.Buffer
	r := newTTYReporter(&buf)

	r.Report(compose.Event{Command: "api", Phase: compose.PhaseStarting})
	r.Report(compose.Event{Command: "worker", Phase: compose.PhaseStarting})
	r.Report(compose.Event{Command: "api", Phase: compose.PhaseRunning})
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := buf.String()
	// Both commands and their latest phase labels appear in the repainted block.
	for _, want := range []string{"api", "worker", "Starting", "Running"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in rendered trace:\n%q", want, out)
		}
	}
	// The renderer repaints in place using cursor-up controls rather than
	// appending fresh blocks each time.
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI cursor controls in rendered trace:\n%q", out)
	}
}

// TestTTYReporterFailureIsSticky verifies that once a command fails during an
// operation, a later non-failure phase does not repaint it as success: the final
// frame keeps the failure kind and detail visible. This is the case the original
// report hit — a recreate whose stop failed was masked by the idempotent
// "running" the start phase reports for the still-running command.
func TestTTYReporterFailureIsSticky(t *testing.T) {
	var buf bytes.Buffer
	r := newTTYReporter(&buf)

	exit := 0
	r.Report(compose.Event{Command: "shell", Phase: compose.PhaseStopping})
	r.Report(compose.Event{
		Command: "shell",
		Phase:   compose.PhaseError,
		Err:     errors.New(`stop command "shell" for recreate: monitor unreachable`),
	})
	// The start phase later reports the still-running command as running; this
	// must not overwrite the failure.
	r.Report(compose.Event{Command: "shell", Phase: compose.PhaseRunning, ExitCode: &exit})
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "✘") {
		t.Fatalf("expected failure glyph in trace:\n%q", out)
	}
	if !strings.Contains(out, "monitor unreachable") {
		t.Fatalf("expected the failure detail (its kind) in trace:\n%q", out)
	}
	// The masked "running" phase must never have been rendered.
	if strings.Contains(out, "Running") {
		t.Fatalf("a failed command must not be repainted as Running:\n%q", out)
	}
}

func TestProgressMarkerByCategory(t *testing.T) {
	// Each status category gets a distinct glyph. lipgloss emits no color codes
	// to a non-terminal test stdout, so the marker is the bare glyph.
	cases := map[compose.Phase]string{
		compose.PhaseCreated:   "◌", // pending
		compose.PhaseRecreated: "◌",
		compose.PhaseUnchanged: "◌",
		compose.PhaseRunning:   "●", // running
		compose.PhaseExited:    "✔", // completed
		compose.PhaseStopped:   "✔",
		compose.PhaseRemoved:   "✔",
		compose.PhaseSkipped:   "⊘", // skipped
		compose.PhaseError:     "✘", // failed
		compose.PhaseFailed:    "✘",
	}
	for phase, want := range cases {
		if got := progressMarker(phase, 0); !strings.Contains(got, want) {
			t.Errorf("progressMarker(%q) = %q, want glyph %q", phase, got, want)
		}
	}
	// Pending must not reuse the running/completed glyph (the original complaint).
	pending := progressMarker(compose.PhaseCreated, 0)
	if strings.Contains(pending, "✔") || strings.Contains(pending, "●") {
		t.Errorf("pending marker %q must differ from running/completed glyphs", pending)
	}
	// In-progress phases animate with a spinner frame.
	if got := progressMarker(compose.PhaseStarting, 0); !strings.Contains(got, spinnerFrames[0]) {
		t.Errorf("in-progress marker %q should use a spinner frame", got)
	}
}

func TestResultErrHelpers(t *testing.T) {
	boom := errors.New("boom")

	if err := UpResultErr(&compose.UpResult{}); err != nil {
		t.Errorf("clean up result should be nil, got %v", err)
	}
	up := &compose.UpResult{
		Starts: []compose.StartOutcome{{Command: "api", Err: boom}},
	}
	if err := UpResultErr(up); err == nil {
		t.Errorf("up with a failed start should return an error")
	}

	if err := StopResultErr(nil); err != nil {
		t.Errorf("empty stop result should be nil, got %v", err)
	}
	stops := []compose.StopOutcome{{Command: "api"}, {Command: "db", Err: boom}}
	if err := StopResultErr(stops); err == nil {
		t.Errorf("stop with a failure should return an error")
	}

	down := &compose.DownResult{
		Removes: []compose.RemoveOutcome{{Command: "api", Err: boom}},
	}
	if err := DownResultErr(down); err == nil {
		t.Errorf("down with a failed remove should return an error")
	}
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

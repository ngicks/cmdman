package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/logdriver"
)

// hookEvent is an event of the run of hook scratch's create_pre for web-2.
func hookEvent(phase compose.Phase) compose.Event {
	return compose.Event{
		Command:    "web-2",
		Phase:      phase,
		ScaleIndex: 2,
		Hook:       "scratch",
		Lifecycle:  compose.LifecycleCreatePre,
		Exec:       "abc-proj-web-2.hook.scratch.create_pre",
	}
}

func TestJSONReporterEmitsHookEvents(t *testing.T) {
	var buf bytes.Buffer
	r := newJSONReporter(&buf, "up")

	output := hookEvent(compose.PhaseHookOutput)
	output.Stream = logdriver.StreamStderr
	output.Line = "making dir"
	succeeded := hookEvent(compose.PhaseHookSucceeded)
	exit := 0
	succeeded.ExitCode = &exit
	r.Report(hookEvent(compose.PhaseHookRunning))
	r.Report(output)
	r.Report(succeeded)
	r.Report(compose.Event{Command: "web-2", Phase: compose.PhaseCreated})

	lines := splitNonEmptyLines(buf.String())
	if len(lines) != 4 {
		t.Fatalf("expected 4 JSONL lines, got %d:\n%s", len(lines), buf.String())
	}
	got := make([]progressLine, len(lines))
	for i, l := range lines {
		if err := json.Unmarshal([]byte(l), &got[i]); err != nil {
			t.Fatalf("invalid JSON line %q: %v", l, err)
		}
	}

	for _, pl := range got[:3] {
		if pl.Hook != "scratch" || pl.Lifecycle != "create_pre" || pl.ScaleIndex != 2 ||
			pl.Exec != "abc-proj-web-2.hook.scratch.create_pre" || pl.Command != "web-2" {
			t.Errorf("hook line lacks its hook run: %+v", pl)
		}
	}
	if got[0].Phase != "hook-running" || got[0].Terminal {
		t.Errorf("running line wrong: %+v", got[0])
	}
	if got[1].Phase != "hook-output" || got[1].Terminal ||
		got[1].Stream != "stderr" || got[1].Line != "making dir" {
		t.Errorf("output line wrong: %+v", got[1])
	}
	if got[2].Phase != "hook-succeeded" || !got[2].Terminal ||
		got[2].ExitCode == nil || *got[2].ExitCode != 0 {
		t.Errorf("succeeded line wrong: %+v", got[2])
	}
	for _, field := range []string{`"hook"`, `"lifecycle"`, `"exec"`, `"line"`, `"scaleIndex"`} {
		if strings.Contains(lines[3], field) {
			t.Errorf("a replica event should omit %s: %s", field, lines[3])
		}
	}
}

func TestTTYReporterGivesHookRunsTheirOwnLine(t *testing.T) {
	var buf bytes.Buffer
	r := newTTYReporter(&buf)

	r.Report(compose.Event{Command: "web-2", Phase: compose.PhaseCreating})
	r.Report(hookEvent(compose.PhaseHookRunning))
	output := hookEvent(compose.PhaseHookOutput)
	output.Line = "making \x1b[31mdir"
	r.Report(output)

	r.mu.Lock()
	buf.Reset()
	r.render()
	running := buf.String()
	r.mu.Unlock()
	if !strings.Contains(running, "web-2 hook scratch.create_pre") {
		t.Fatalf("hook run should have its own line:\n%q", running)
	}
	if !strings.Contains(running, "making [31mdir") {
		t.Fatalf("a running hook should show its output, control characters dropped:\n%q",
			running)
	}
	if !strings.Contains(running, "Creating") {
		t.Fatalf("the replica line should keep its own phase:\n%q", running)
	}

	r.Report(hookEvent(compose.PhaseHookSucceeded))
	r.Report(compose.Event{Command: "web-2", Phase: compose.PhaseCreated})
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	final := lastFrame(buf.String())
	for _, want := range []string{"Hook-succeeded", "Created"} {
		if !strings.Contains(final, want) {
			t.Errorf("final frame missing %q:\n%q", want, final)
		}
	}
	if strings.Contains(final, "making") {
		t.Errorf("a finished hook should not keep its output on the line:\n%q", final)
	}
	if got := len(splitNonEmptyLines(final)); got != 2 {
		t.Errorf("expected 2 lines (replica and hook run), got %d:\n%q", got, final)
	}
}

func TestTTYReporterHookFailureShowsError(t *testing.T) {
	var buf bytes.Buffer
	r := newTTYReporter(&buf)

	failed := hookEvent(compose.PhaseHookFailed)
	failed.Err = errors.New("hook command exited with code 3")
	r.Report(hookEvent(compose.PhaseHookRunning))
	r.Report(failed)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	final := lastFrame(buf.String())
	if !strings.Contains(final, "✘") || !strings.Contains(final, "exited with code 3") {
		t.Fatalf("expected the failure glyph and detail:\n%q", final)
	}
}

func TestOutputSnippetCutsLongLines(t *testing.T) {
	long := strings.Repeat("x", maxOutputRunes+10)
	got := outputSnippet(long)
	if n := len([]rune(got)); n != maxOutputRunes {
		t.Fatalf("snippet has %d runes, want %d: %q", n, maxOutputRunes, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a cut snippet should end in an ellipsis: %q", got)
	}
}

func TestProgressMarkerHookPhases(t *testing.T) {
	cases := map[compose.Phase]string{
		compose.PhaseHookSucceeded: "✔",
		compose.PhaseHookFailed:    "✘",
		compose.PhaseHookWarning:   "!",
		compose.PhaseHookRunning:   spinnerFrames[0],
	}
	for phase, want := range cases {
		if got := progressMarker(phase, 0); !strings.Contains(got, want) {
			t.Errorf("progressMarker(%q) = %q, want glyph %q", phase, got, want)
		}
	}
}

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

func TestTTYReporterHookIgnoredSettlesTheLine(t *testing.T) {
	var buf bytes.Buffer
	r := newTTYReporter(&buf)

	ignored := hookEvent(compose.PhaseHookIgnored)
	ignored.Err = errors.New("hook command exited with code 3")
	r.Report(hookEvent(compose.PhaseHookRunning))
	r.Report(ignored)

	r.mu.Lock()
	inProgress := r.hasInProgress()
	r.mu.Unlock()
	if inProgress {
		t.Fatal("an ignored hook run should leave no line in progress")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	final := lastFrame(buf.String())
	if !strings.Contains(final, "Hook-ignored") || !strings.Contains(final, "exited with code 3") {
		t.Fatalf("expected the ignored phase and its detail:\n%q", final)
	}
	if strings.Contains(final, "✘") {
		t.Fatalf("an ignored hook run should not wear the failure glyph:\n%q", final)
	}
	if got := len(splitNonEmptyLines(final)); got != 1 {
		t.Errorf("expected the hook run on 1 line, got %d:\n%q", got, final)
	}
}

func TestTTYReporterKeepsHookStepsOnTheHookLine(t *testing.T) {
	var buf bytes.Buffer
	r := newTTYReporter(&buf)

	r.Report(compose.Event{Command: "web-2", Phase: compose.PhaseCreated})
	r.Report(hookEvent(compose.PhaseHookRunning))
	r.Report(hookEvent(compose.PhaseHookSucceeded))
	// A second run of the same hook event opens a new step on the hook's line.
	r.Report(hookEvent(compose.PhaseHookRunning))

	r.mu.Lock()
	defer r.mu.Unlock()
	replica := r.lines["web-2"]
	if len(replica) != 1 || replica[0].phase != compose.PhaseCreated {
		t.Fatalf("hook steps must not land on the replica line: %+v", replica)
	}
	hook := r.lines["web-2 hook scratch.create_pre"]
	if len(hook) != 2 || hook[1].phase != compose.PhaseHookRunning {
		t.Fatalf("want the settled run and the new one on the hook line: %+v", hook)
	}
}

func TestTTYReporterShowsALaterRunAfterAFailedRun(t *testing.T) {
	var buf bytes.Buffer
	r := newTTYReporter(&buf)

	release := func(phase compose.Phase) compose.Event {
		ev := hookEvent(phase)
		ev.Lifecycle = compose.LifecycleStopPre
		ev.Exec = "abc-proj-web-2.hook.scratch.stop_pre"
		return ev
	}
	failed := release(compose.PhaseHookFailed)
	failed.Err = errors.New("release resource scratch: exited with code 3")
	succeeded := release(compose.PhaseHookSucceeded)
	exit := 0
	succeeded.ExitCode = &exit
	r.Report(release(compose.PhaseHookRunning))
	r.Report(failed)
	// The retry of the release runs the same hook event again and works.
	r.Report(release(compose.PhaseHookRunning))
	r.Report(succeeded)

	r.mu.Lock()
	hook := r.lines["web-2 hook scratch.stop_pre"]
	inProgress := r.hasInProgress()
	r.mu.Unlock()
	if len(hook) != 2 || hook[0].phase != compose.PhaseHookFailed ||
		hook[1].phase != compose.PhaseHookSucceeded {
		t.Fatalf("want the failed run and the run that worked on the hook line: %+v", hook)
	}
	if inProgress {
		t.Fatal("a later run that ended should leave no line in progress")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	rows := splitNonEmptyLines(lastFrame(buf.String()))
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (one per run), got %d:\n%q", len(rows), rows)
	}
	if !strings.Contains(rows[0], "Hook-failed") ||
		!strings.Contains(rows[0], "exited with code 3") {
		t.Errorf("the first row should keep the failure: %q", rows[0])
	}
	if !strings.Contains(rows[1], "Hook-succeeded") || !strings.Contains(rows[1], "(exit 0)") {
		t.Errorf("the second row should show the run that worked: %q", rows[1])
	}
}

func TestJSONReporterEmitsHookIgnored(t *testing.T) {
	var buf bytes.Buffer
	r := newJSONReporter(&buf, "down")

	ignored := hookEvent(compose.PhaseHookIgnored)
	ignored.Err = errors.New("hook command exited with code 3")
	exit := 3
	ignored.ExitCode = &exit
	r.Report(ignored)

	var got progressLine
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON line %q: %v", buf.String(), err)
	}
	if got.Phase != "hook-ignored" || !got.Terminal || got.Hook != "scratch" ||
		got.ExitCode == nil || *got.ExitCode != 3 ||
		!strings.Contains(got.Error, "exited with code 3") {
		t.Fatalf("ignored line wrong: %+v", got)
	}
}

// unreleasedEvent reports resource key of web-2, valued value, as unreleased.
func unreleasedEvent(key, value, msg string) compose.Event {
	return compose.Event{
		Command:  "web-2",
		Phase:    compose.PhaseUnreleased,
		Err:      errors.New(msg),
		Resource: key,
		Value:    value,
	}
}

func TestJSONReporterEmitsUnreleased(t *testing.T) {
	var buf bytes.Buffer
	r := newJSONReporter(&buf, "down")

	retried := unreleasedEvent("net", "net-1", "network is in use")
	retried.Retried = true
	r.Report(unreleasedEvent("port", "port-2", "port is in use"))
	r.Report(retried)
	r.Report(compose.Event{Command: "web-2", Phase: compose.PhaseRemoved})

	lines := splitNonEmptyLines(buf.String())
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSONL lines, got %d:\n%s", len(lines), buf.String())
	}
	var port, net progressLine
	if err := json.Unmarshal([]byte(lines[0]), &port); err != nil {
		t.Fatalf("invalid JSON line %q: %v", lines[0], err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &net); err != nil {
		t.Fatalf("invalid JSON line %q: %v", lines[1], err)
	}
	if port.Op != "down" || port.Command != "web-2" || port.Phase != "unreleased" ||
		!port.Terminal || port.Resource != "port" || port.Value != "port-2" ||
		port.Error != "port is in use" || port.Retried {
		t.Errorf("unreleased line wrong: %+v", port)
	}
	if !net.Retried || net.Resource != "net" {
		t.Errorf("retried unreleased line wrong: %+v", net)
	}
	for _, field := range []string{`"resource"`, `"value"`, `"retried"`} {
		if strings.Contains(lines[2], field) {
			t.Errorf("a replica event should omit %s: %s", field, lines[2])
		}
	}
}

func TestTTYReporterGivesEachUnreleasedResourceALine(t *testing.T) {
	var buf bytes.Buffer
	r := newTTYReporter(&buf)

	removeFailed := compose.Event{
		Command: "web-2",
		Phase:   compose.PhaseError,
		Err:     errors.New("remove failed"),
	}
	r.Report(compose.Event{Command: "web-2", Phase: compose.PhaseRemoving})
	r.Report(removeFailed)
	r.Report(unreleasedEvent("port", "port-2", "port is in use\nsecond line"))
	r.Report(unreleasedEvent("net", "net-1", "network is in use"))
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	final := lastFrame(buf.String())
	rows := splitNonEmptyLines(final)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (web-2 and one per resource), got %d:\n%q", len(rows), final)
	}
	// The marker may be wrapped in color codes; the text after it is plain.
	for i, want := range []string{
		" web-2: resource port (port-2) not released: port is in use",
		" web-2: resource net (net-1) not released: network is in use",
	} {
		row := rows[i+1]
		if !strings.Contains(row, "!") || !strings.HasSuffix(row, want) {
			t.Errorf("row %d = %q, want the ! marker and %q", i+1, row, want)
		}
	}
	if strings.Contains(final, "second line") {
		t.Errorf("an unreleased line should show the first line of the error only:\n%q", final)
	}
}

func TestProgressMarkerHookPhases(t *testing.T) {
	cases := map[compose.Phase]string{
		compose.PhaseHookSucceeded: "✔",
		compose.PhaseHookFailed:    "✘",
		compose.PhaseHookWarning:   "!",
		compose.PhaseHookIgnored:   "-",
		compose.PhaseHookRunning:   spinnerFrames[0],
		compose.PhaseUnreleased:    "!",
	}
	for phase, want := range cases {
		if got := progressMarker(phase, 0); !strings.Contains(got, want) {
			t.Errorf("progressMarker(%q) = %q, want glyph %q", phase, got, want)
		}
	}
}

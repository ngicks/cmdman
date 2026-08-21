package cmdman_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The initial title and the prefix of the continuously changing titles the
// supervised command wears, in that order. They differ from their very first
// character on purpose: the terminal is repainted by diffing against what it
// already shows, so two titles sharing a prefix could reach the captured stream
// as the tail that changed, which the transcript search would never find.
const (
	liveTitleFirst  = "alphaready"
	liveTitleSecond = "omega-shifted"
)

// TestTUIWidget_SwitcherShowsRetitleWithoutLifecycleEvent is the plan's success
// criterion 1 end to end: a supervised TTY command retitles itself mid-run, and
// the switcher shows the new title with **no** eventlog lifecycle event
// happening in between.
//
// Both halves are the assertion. The re-list the widget has always done is
// driven by the event log, so a render alone would prove nothing — an event
// arriving in the same window would explain it just as well. The event log
// being byte-for-byte what it was is what leaves the runtime-state stream as
// the only thing that could have carried the title across.
//
// The widget runs under a PTY of its own rather than as a tmux window: what is
// asserted here is what the switcher rendered, and that is the harness the
// other widget render tests use — tmux is for the landing gestures, which
// nothing here performs.
func TestTUIWidget_SwitcherShowsRetitleWithoutLifecycleEvent(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	// The command holds its first title until the test releases it with a marker
	// file, then retitles continuously, so the changes provably happen after the
	// switcher is on screen. A sleep here would race the push under test.
	stage := t.TempDir()
	script := filepath.Join(stage, "retitle.sh")
	must(t, os.WriteFile(script, fmt.Appendf(nil, `#!/bin/sh
printf '\033]0;%[2]s\007'
until [ -e "%[1]s/retitle" ]; do sleep 0.05; done
i=0
while :; do
  printf '\033]0;%[3]s-%%s\007' "$i"
  i=$((i + 1))
  sleep 0.05
done
`, stage, liveTitleFirst, liveTitleSecond), 0o755))

	wd := composeWorkdir(t)
	const project = "swlive"
	// tty: true is the whole premise — title capture is TTY-only (D35), so
	// without a pseudo-terminal no OSC sequence ever reaches the monitor. One
	// command, so the only lifecycle the event log can record is its own.
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  ticker:
    tty: true
    args: [/bin/sh, %q]
`, project, script))
	t.Cleanup(func() { cleanupProject(context.Background(), env, wd, project) })

	if _, stderr, err := env.exec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "up",
	); err != nil {
		t.Fatalf("compose up failed: %v\nstderr:\n%s", err, stderr)
	}

	w := startWidget(t, ctx, env, wd, "switcher")
	// The project's head is the directory it sits in (D44); waiting on it first
	// makes a discovery failure read as itself rather than as a missing title.
	w.waitFor(t, filepath.Base(wd), 15*time.Second)
	w.waitFor(t, liveTitleFirst, 15*time.Second)

	// Only a command the list shows as running renders a title at all, so the
	// run's lifecycle is over by now — but the log is written by the monitor,
	// not by the row, so wait for the "running" entry to actually be in it
	// before reading. Otherwise the comparison below could pass by having read
	// a log the lifecycle had not finished writing.
	before := waitForEventLogEntry(t, ctx, env, "running")

	must(t, os.WriteFile(filepath.Join(stage, "retitle"), nil, 0o600))

	// The command keeps changing its title faster than the server's 150ms
	// throttle interval. The switcher must still receive a bounded update;
	// waiting for the producer to become quiet would starve this indefinitely.
	w.waitFor(t, liveTitleSecond, 15*time.Second)

	if after := env.run(ctx, "events", "--no-follow"); after != before {
		t.Errorf(
			"the event log changed across the retitle, so the render is no proof "+
				"of the stream;\nbefore:\n%s\nafter:\n%s",
			before, after,
		)
	}
	w.quit(t)
}

// waitForEventLogEntry returns the whole event log once it carries an entry of
// that type, so a caller reading the log as a "nothing happened since" baseline
// is comparing against a log the lifecycle it cares about has already reached.
func waitForEventLogEntry(
	t *testing.T,
	ctx context.Context,
	env *testEnv,
	eventType string,
) string {
	t.Helper()
	deadline := time.Now().Add(defaultTimeout)
	for {
		log := env.run(ctx, "events", "--no-follow")
		if _, ok := collectEventTypes(t, log)[eventType]; ok {
			return log
		}
		if time.Now().After(deadline) {
			t.Fatalf("event log never recorded a %q event; log:\n%s", eventType, log)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

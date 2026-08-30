package cmdman_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestAttach_ReplaysReportedCwd drives the whole OSC 7 path through the real
// binary: a TTY command reports a working directory, the monitor latches it, and
// a fresh attach - a process that never saw the byte the child wrote - gets the
// sequence re-emitted so its terminal learns the directory too.
//
// Nothing but the synthesized re-emit can supply that sequence here. The
// scrollback a TTY attach replays is a render of the monitor's screen mirror,
// i.e. cells, which cannot carry an OSC; the raw bytes the child wrote were
// rotated out of the 4 KiB ring (the fallback replay) by the padding; and the
// child is asleep by the time the attach subscribes, so no live output follows.
func TestAttach_ReplaysReportedCwd(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	const reported = "/e2e/osc7/reported"
	script := `printf '\033]7;file://localhost` + reported + `\007'
n=0
pad=paddingpaddingpaddingpaddingpaddingpaddingpaddingpaddingpaddingpaddingpadding
while [ $n -lt 400 ]; do
  n=$((n+1))
  printf 'PAD %d %s\n' "$n" "$pad"
done
printf 'PAD_DONE\n'
sleep 300`

	// -w somewhere else: the seeded baseline differs from what the child
	// reports, so the poll below only passes once the OSC 7 has replaced it.
	id := env.run(ctx, "run", "-t", "-n", "attach-cwd-reported", "-w", t.TempDir(),
		"--scrollback-bytes", "4096", "--", "/bin/sh", "-c", script)
	t.Cleanup(func() { env.cleanupCommand(context.Background(), id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	pollOutput(ctx, env, reported, "ls", "--format", "{{.Cwd}}")
	// The padding has to be through the monitor before the attach, or the ring
	// could still hold the child's own OSC 7 and answer for the re-emit.
	waitUntil(t, defaultTimeout, func() bool {
		out, _, err := env.exec(ctx, "capture-screen", id)
		return err == nil && strings.Contains(out, "PAD_DONE")
	}, "the padded output never reached the screen")

	attach := env.Cmd("attach", id).StartPTY(ctx, t)
	attach.Expect(t,
		regexp.QuoteMeta("\x1b]7;file://localhost"+reported+"\x1b\\"), defaultTimeout)
	detachAttach(t, attach)
}

// TestAttach_ReplaysSeededCwd covers the other half: a command that never says
// anything still tells an attaching viewer where it is running, from the
// directory it was configured with.
func TestAttach_ReplaysSeededCwd(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	dir := t.TempDir()
	id := env.run(ctx, "run", "-t", "-n", "attach-cwd-seeded", "-w", dir,
		"--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(context.Background(), id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	pollOutput(ctx, env, dir, "ls", "--format", "{{.Cwd}}")

	attach := env.Cmd("attach", id).StartPTY(ctx, t)
	// A t.TempDir path is plain ASCII, so the payload is the directory appended
	// to the scheme and host verbatim - nothing in it percent-encodes.
	attach.Expect(t, regexp.QuoteMeta("\x1b]7;file://localhost"+dir+"\x1b\\"), defaultTimeout)
	detachAttach(t, attach)
}

// detachAttach leaves with the default detach keys, which end the attach
// without touching the command, and requires the exit to be prompt.
func detachAttach(t *testing.T, s *Session) {
	t.Helper()

	s.Send(detachKeys)
	res, ok := s.WaitWithin(t, 5*time.Second)
	if !ok {
		t.Fatal("attach did not exit after the detach keys")
	}
	if res.Err != nil {
		t.Fatalf("attach exited with error: %v", res.Err)
	}
}

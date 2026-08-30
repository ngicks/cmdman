package cmdman_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman"
)

// writeHookConfig installs a cmdman config file carrying a default_hooks map at
// the path the test env already points $CMDMAN_CONF at, so the monitor spawned
// by a later `run` picks it up.
func writeHookConfig(t *testing.T, env *testEnv, hooksJSON string) {
	t.Helper()
	writeHookConfigAt(t, env.confPath, hooksJSON)
}

// writeHookConfigAt is writeHookConfig for a path the test names itself - the
// only way to exercise the config file as something other than $CMDMAN_CONF.
func writeHookConfigAt(t *testing.T, path, hooksJSON string) {
	t.Helper()
	conf := `{"default_hooks": ` + hooksJSON + `}`
	must(t, os.WriteFile(path, []byte(conf), 0o600))
}

// bellHookWriting returns a default_hooks map whose bell hook writes
// "<event>|<command id>" to out, so a passing assertion means the monitor
// loaded the config, filtered the bell, and ran the hook with its environment.
func bellHookWriting(out string) string {
	return `{
  "bell": {
    "exec": [
      "/bin/sh", "-c",
      "printf '%s|%s' \"$` + cmdman.ENV_CMDMAN_HOOK_EVENT + `\" \"$` +
		cmdman.ENV_CMDMAN_CMD_ID + `\" > \"$0\"",
      "` + out + `"
    ]
  }
}`
}

// waitForHookOutput waits for the bell hook to write out and asserts it saw the
// event and the command it belongs to.
func waitForHookOutput(t *testing.T, out, id string) {
	t.Helper()
	waitUntil(t, defaultTimeout, func() bool {
		_, err := os.Stat(out)
		return err == nil
	}, "bell hook never ran (expected it to write %q)", out)

	data, err := os.ReadFile(out)
	must(t, err)
	if got, want := string(data), "bell|"+id; got != want {
		t.Fatalf("hook environment: got %q, want %q", got, want)
	}
}

// TestHooks_BellRunsConfiguredCommand rings a BEL from a supervised TTY command
// and verifies the configured hook argv actually ran, with the event data and
// the command identity in its environment.
func TestHooks_BellRunsConfiguredCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	out := filepath.Join(t.TempDir(), "bell-hook.out")
	writeHookConfig(t, env, bellHookWriting(out))

	id := env.run(ctx, "run", "-t", "--", "/bin/sh", "-c", `printf '\a'; sleep 30`)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	waitForHookOutput(t, out, id)
}

// TestHooks_ConfigFlagReachesMonitor is the same assertion sourced from
// --config instead of $CMDMAN_CONF. The hooks live in a file nothing in the
// environment points at, so they can only reach the monitor if `run` forwards
// its --config to the process it re-execs: a child inherits the environment,
// which is what makes the sibling case above pass without any forwarding.
func TestHooks_ConfigFlagReachesMonitor(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	dir := t.TempDir()
	out := filepath.Join(dir, "bell-hook.out")
	confPath := filepath.Join(dir, "config.json")
	writeHookConfigAt(t, confPath, bellHookWriting(out))

	id := env.run(ctx,
		"--config", confPath,
		"run", "-t", "--", "/bin/sh", "-c", `printf '\a'; sleep 30`)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	// Deliberately without --config: only the spawning invocation needs it, and
	// the state these poll for is written by the monitor.
	env.waitForState(ctx, id, "running", defaultTimeout)

	waitForHookOutput(t, out, id)
}

// bellMarker is printed right behind every bell the test command rings, so a
// viewer that receives it has necessarily been handed the bell before it - or
// had it removed. Asserting on the bell alone cannot tell "blocked" from
// "nothing arrived at all".
//
// The command erases the marker from its line as soon as it prints it, which is
// what makes the marker mean "live". An attach opens by replaying scrollback,
// and for a TTY command that replay is a re-render of the emulator's screen: it
// would carry markers the command printed before this attach existed, but never
// a bell byte. Keeping the screen blank leaves the marker only in the live byte
// stream, so receiving one proves output is flowing through the filter now.
const bellMarker = "ding"

// bellLoop rings the bell, prints the marker, then wipes the line.
const bellLoop = `while :; do printf '\007` + bellMarker + `\r\033[K'; sleep 0.2; done`

// TestHooks_BlockKeepsBellFromViewers checks both halves of the block action:
// with no hooks the bell reaches an attached viewer, and with the bell blocked
// it does not.
func TestHooks_BlockKeepsBellFromViewers(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)

	passthrough := newTestEnv(t)
	if got := attachedOutput(t, ctx, passthrough, "hook-bell-pass"); !strings.ContainsRune(
		got, '\a',
	) {
		t.Fatalf("expected the bell to reach the viewer by default, got %q", got)
	}

	blocked := newTestEnv(t)
	writeHookConfig(t, blocked, `{"bell": {"action": "block"}}`)
	got := attachedOutput(t, ctx, blocked, "hook-bell-block")
	// The marker first: without it the bell assertion below would pass on an
	// attach that never delivered anything.
	if !strings.Contains(got, bellMarker) {
		t.Fatalf("viewer never received %q, so the bell check says nothing: %q", bellMarker, got)
	}
	if strings.ContainsRune(got, '\a') {
		t.Fatalf("blocked bell reached the viewer: %q", got)
	}
}

// attachedOutput starts a TTY command that rings the bell repeatedly, attaches
// to it until markers arrive live, and returns what the viewer received.
func attachedOutput(t *testing.T, ctx context.Context, env *testEnv, name string) string {
	t.Helper()

	id := env.run(ctx, "run", "-t", "-n", name, "--", "/bin/sh", "-c", bellLoop)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	sess := env.Cmd("attach", id).StartPTY(ctx, t)

	// The bell of each pair is ordered ahead of the marker that proves the pair
	// arrived, so one marker already means one bell went through the filter.
	// Two, because the loop's \r overwrites in place: a replayed screen can hold
	// at most one marker even if the erase above ever stops working, which keeps
	// this a liveness gate rather than a restatement of the replay.
	waitUntil(t, defaultTimeout, func() bool {
		return strings.Count(sess.Output(), bellMarker) >= 2
	}, "attach never streamed %q live; received %q", bellMarker, sess.Output())

	sess.Send(detachKeys)
	res, exited := sess.WaitWithin(t, 3*time.Second)
	if !exited {
		t.Fatal("attach did not exit")
	}
	if res.Err != nil {
		t.Fatalf("attach exited with error: %v", res.Err)
	}

	// Not the trimmed Result: the bell the caller looks for sits at the head of
	// a marker pair, and what surrounds it is not this test's to assume.
	return sess.Output()
}

package cmdman_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/ngicks/cmdman/cmdman"
)

// writeHookConfig installs a cmdman config file carrying a default_hooks map at
// the path the test env already points $CMDMAN_CONF at, so the monitor spawned
// by a later `run` picks it up.
func writeHookConfig(t *testing.T, env *testEnv, hooksJSON string) {
	t.Helper()
	conf := `{"default_hooks": ` + hooksJSON + `}`
	must(t, os.WriteFile(env.confPath, []byte(conf), 0o600))
}

// TestHooks_BellRunsConfiguredCommand rings a BEL from a supervised TTY command
// and verifies the configured hook argv actually ran, with the event data and
// the command identity in its environment.
func TestHooks_BellRunsConfiguredCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	out := filepath.Join(t.TempDir(), "bell-hook.out")
	writeHookConfig(t, env, `{
  "bell": {
    "exec": [
      "/bin/sh", "-c",
      "printf '%s|%s' \"$`+cmdman.ENV_CMDMAN_HOOK_EVENT+`\" \"$`+
		cmdman.ENV_CMDMAN_CMD_ID+`\" > \"$0\"",
      "`+out+`"
    ]
  }
}`)

	id := env.run(ctx, "run", "-t", "--", "/bin/sh", "-c", `printf '\a'; sleep 30`)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

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

	attach := exec.CommandContext(ctx, cmdmanBin, "attach", id)
	attach.Env = append(
		os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
	)

	ptmx, err := pty.Start(attach)
	must(t, err)
	defer ptmx.Close()

	var output lockedBuffer
	doneRead := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, ptmx)
		close(doneRead)
	}()

	// The bell of each pair is ordered ahead of the marker that proves the pair
	// arrived, so one marker already means one bell went through the filter.
	// Two, because the loop's \r overwrites in place: a replayed screen can hold
	// at most one marker even if the erase above ever stops working, which keeps
	// this a liveness gate rather than a restatement of the replay.
	waitUntil(t, defaultTimeout, func() bool {
		return strings.Count(output.String(), bellMarker) >= 2
	}, "attach never streamed %q live; received %q", bellMarker, output.String())

	if _, err := ptmx.Write([]byte{0x10, 0x11}); err != nil {
		t.Fatalf("send detach keys: %v", err)
	}
	waitAttachExit(t, attach, 3*time.Second)
	_ = ptmx.Close()
	<-doneRead

	return output.String()
}

// lockedBuffer lets the poller above read what the viewer has received so far
// while the io.Copy goroutine is still appending to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

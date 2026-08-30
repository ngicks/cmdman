package cmdman_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/ngicks/cmdman/cmdman"
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

	attach := startAttachPty(ctx, t, env, id)
	attach.waitContains(t, "\x1b]7;file://localhost"+reported+"\x07")
	attach.detach(t)
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

	attach := startAttachPty(ctx, t, env, id)
	// A t.TempDir path is plain ASCII, so the payload is the directory appended
	// to the scheme and host verbatim - nothing in it percent-encodes.
	attach.waitContains(t, "\x1b]7;file://localhost"+dir+"\x07")
	attach.detach(t)
}

// attachPty is a `cmdman attach` running on a pty of its own, with everything
// it writes accumulated as it arrives: the replay lands before any test could
// ask for it, so it has to be captured from the start rather than read on
// demand.
type attachPty struct {
	cmd  *exec.Cmd
	ptmx *os.File

	mu  sync.Mutex
	buf bytes.Buffer
}

func startAttachPty(ctx context.Context, t *testing.T, env *testEnv, id string) *attachPty {
	t.Helper()

	cmd := exec.CommandContext(ctx, cmdmanBin, "attach", id)
	cmd.Env = append(
		os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
	)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start attach pty: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close() })

	a := &attachPty{cmd: cmd, ptmx: ptmx}
	go func() {
		b := make([]byte, 8192)
		for {
			n, rerr := ptmx.Read(b)
			if n > 0 {
				a.mu.Lock()
				a.buf.Write(b[:n])
				a.mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	return a
}

func (a *attachPty) output() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.buf.String()
}

// waitContains polls what the attach has written until want appears, failing
// with everything seen when it never does.
func (a *attachPty) waitContains(t *testing.T, want string) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		if strings.Contains(a.output(), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("attach never wrote %q; got:\n%q", want, a.output())
}

// detach leaves with the default detach keys, which end the attach without
// touching the command.
func (a *attachPty) detach(t *testing.T) {
	t.Helper()

	if _, err := a.ptmx.Write([]byte{0x10, 0x11}); err != nil {
		t.Fatalf("send detach keys: %v", err)
	}
	waitAttachExit(t, a.cmd, 5*time.Second)
}

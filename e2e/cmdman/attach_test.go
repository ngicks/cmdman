package cmdman_test

import (
	"strings"
	"testing"
	"time"
)

// detachKeys is the default detach sequence, ctrl-p ctrl-q.
const detachKeys = "\x10\x11"

func TestAttach_DetachKeysExitWithoutStoppingCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.Cmd("run", "-t", "-n", "attach-detach", "--", "/bin/sh", "-c", "sleep 300").
		Run(ctx, t)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	sess := env.Cmd("attach", id).StartPTY(ctx, t)
	// Attaching to an idle command prints nothing to wait on; the pause is for
	// attach to have its stdin forwarder up before the keys arrive.
	time.Sleep(300 * time.Millisecond)
	sess.Send(detachKeys)

	if res := sess.Wait(t); res.Err != nil {
		t.Fatalf("attach exited with error: %v", res.Err)
	}
	env.waitForState(ctx, id, "running", defaultTimeout)
}

func TestAttach_ExitsWhenCommandStopsFromCtrlC(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.Cmd("run", "-t", "-n", "attach-sigint", "--", "/bin/sh", "-c", "sleep 300").
		Run(ctx, t)
	env.waitForState(ctx, id, "running", defaultTimeout)

	// --auto-exit: this test asserts the non-sticky behavior where attach exits
	// once the command stops. Without it, attach defaults to sticky and drops to
	// the restart prompt instead of exiting when ctrl-c stops the command.
	sess := env.Cmd("attach", "--auto-exit", id).StartPTY(ctx, t)
	time.Sleep(300 * time.Millisecond)
	sess.Send("\x03")

	if res := sess.Wait(t); res.Err != nil {
		t.Fatalf("attach exited with error: %v", res.Err)
	}
	env.waitForState(ctx, id, "exited", defaultTimeout)
}

func TestAttach_DetachRestoresShellTtyMode(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.Cmd("run", "-t", "-n", "attach-tty", "--", "/bin/sh", "-c", "sleep 300").
		Run(ctx, t)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	sess := env.Tool("/bin/sh", "-lc", ttyModeScript(cmdmanBin+" attach "+id)).StartPTY(ctx, t)
	time.Sleep(300 * time.Millisecond)
	sess.Send(detachKeys)

	assertTtyModeRestored(t, sess.Wait(t), "attach detach")
}

func TestAttach_CtrlCRestoresShellTtyMode(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.Cmd("run", "-t", "-n", "attach-tty-sigint", "--", "/bin/sh", "-c", "sleep 300").
		Run(ctx, t)
	env.waitForState(ctx, id, "running", defaultTimeout)

	// --auto-exit: assert the non-sticky path where attach exits (status 0) once
	// the command stops, so the shell can capture the restored tty mode. Sticky
	// would drop to the restart prompt and never return to the script.
	script := ttyModeScript(cmdmanBin + " attach --auto-exit " + id)
	sess := env.Tool("/bin/sh", "-lc", script).StartPTY(ctx, t)
	time.Sleep(300 * time.Millisecond)
	sess.Send("\x03")

	assertTtyModeRestored(t, sess.Wait(t), "attach ctrl-c")
}

// ttyModeScript brackets attachCmd with the shell's own view of its terminal
// mode, so what the shell is left holding is observable from outside.
func ttyModeScript(attachCmd string) string {
	return strings.Join([]string{
		"before=$(stty -g)",
		attachCmd,
		"status=$?",
		"after=$(stty -g)",
		"printf 'STATUS:%s\\nBEFORE:%s\\nAFTER:%s\\n' \"$status\" \"$before\" \"$after\"",
	}, "; ")
}

func assertTtyModeRestored(t *testing.T, res Result, what string) {
	t.Helper()

	text := res.Stdout
	before := extractMarkedLine(t, text, "BEFORE:")
	after := extractMarkedLine(t, text, "AFTER:")
	status := extractMarkedLine(t, text, "STATUS:")

	if status != "0" {
		t.Fatalf("shell script exited with status %q\noutput:\n%s", status, text)
	}
	if before != after {
		t.Fatalf(
			"tty mode changed across %s\nbefore=%q\nafter=%q\noutput:\n%s",
			what,
			before,
			after,
			text,
		)
	}
}

// TestAttach_ReconstructsScrolledOutScreenFromSnapshot is the end-to-end guard
// for the server-side screen snapshot (D15). A TTY command paints static chrome
// once, then emits enough incremental updates to rotate that chrome out of the
// tiny byte scrollback, then idles. Because the monitor now hands attach a
// snapshot of its persistent screen mirror instead of the raw (rotated) ring,
// the one-time chrome must still arrive on a fresh attach — the exact content
// that "broke when transitioning among commands" in the preview.
func TestAttach_ReconstructsScrolledOutScreenFromSnapshot(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Paint a header once, overflow a 4 KiB scrollback with row-10 updates, idle.
	script := `printf '\033[?1049h\033[2J\033[H'
printf '\033[1;1HHEADER-ONCE-MARKER'
n=0
pad=paddingpaddingpaddingpaddingpaddingpaddingpaddingpaddingpaddingpaddingpadding
while [ $n -lt 400 ]; do
  n=$((n+1))
  printf '\033[10;1HUPDATE %d %s' "$n" "$pad"
done
printf '\033[10;1HFINAL-LINE-MARKER %s' "$pad"
sleep 300`
	id := env.Cmd("run", "-t", "-n", "snap-recon", "--scrollback-bytes", "4096",
		"--", "/bin/sh", "-c", script).Run(ctx, t)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)
	time.Sleep(700 * time.Millisecond) // let the update loop finish and the screen settle

	sess := env.Cmd("attach", id).StartPTY(ctx, t)

	// The snapshot (scrollback replacement) is sent immediately on attach. The
	// header was painted once and has long since rotated out of the byte ring:
	// only the server-side screen mirror can still produce it.
	sess.Expect(t, "HEADER-ONCE-MARKER", 3*time.Second)
	sess.Expect(t, "FINAL-LINE-MARKER", 3*time.Second)

	// Detach without stopping the command.
	sess.Send(detachKeys)
}

// TestAttach_RestartReattachStreamsOutput reproduces the bug where, inside a
// sticky attach (the default and what `compose mux` panes run), pressing 'r'
// at the wait prompt restarts the command but its output never reaches the
// pane — "restart but no reattach". The command prints a marker shortly after
// each start and then exits, so the first attach EOFs to the wait prompt and
// each (re)attach has a live window to stream the marker. After 'r', the
// marker from the restarted run must appear in the pane.
func TestAttach_RestartReattachStreamsOutput(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	const marker = "RUNMARK"
	id := env.Cmd("run", "-t", "-n", "attach-reattach",
		"--", "/bin/sh", "-c", "sleep 0.3; echo "+marker+"; sleep 0.4").Run(ctx, t)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	sess := env.Cmd("attach", id).StartPTY(ctx, t)

	// Wait for the first run to exit and the sticky restart prompt to appear.
	sess.Expect(t, "press 'r' to restart", 5*time.Second)

	// Everything up to here (including the first run's marker) precedes mark;
	// the reattached run's output must show up after it.
	mark := len(sess.Output())
	sess.Send("r")
	sess.ExpectAfter(t, mark, marker, 6*time.Second)

	sess.Send(detachKeys)
	if res := sess.Wait(t); res.Err != nil {
		t.Fatalf("attach exited with error: %v", res.Err)
	}
}

func extractMarkedLine(t *testing.T, text, prefix string) string {
	t.Helper()

	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, prefix); idx >= 0 {
			return strings.TrimPrefix(line[idx:], prefix)
		}
	}
	t.Fatalf("missing prefix %q in output:\n%s", prefix, text)
	return ""
}

package cmdman_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty/v2"
	"github.com/ngicks/cmdman/pkg/cmdman"
)

func TestAttach_DetachKeysExitWithoutStoppingCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-t", "-n", "attach-detach", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "started", defaultTimeout)

	attach := exec.CommandContext(ctx, cmdmanBin, "attach", id)
	attach.Env = append(
		os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
	)

	ptmx, err := pty.Start(attach)
	if err != nil {
		t.Fatalf("start attach pty: %v", err)
	}
	defer ptmx.Close()

	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte{0x10, 0x11}); err != nil {
		t.Fatalf("send detach keys: %v", err)
	}

	waitAttachExit(t, attach, 3*time.Second)
	env.waitForState(ctx, id, "started", defaultTimeout)
}

func TestAttach_ExitsWhenCommandStopsFromCtrlC(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-t", "-n", "attach-sigint", "--", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, id, "started", defaultTimeout)

	attach := exec.CommandContext(ctx, cmdmanBin, "attach", id)
	attach.Env = append(
		os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
	)

	ptmx, err := pty.Start(attach)
	if err != nil {
		t.Fatalf("start attach pty: %v", err)
	}
	defer ptmx.Close()

	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte{0x03}); err != nil {
		t.Fatalf("send ctrl-c: %v", err)
	}

	waitAttachExit(t, attach, 3*time.Second)
	env.waitForState(ctx, id, "exited", defaultTimeout)
}

func TestAttach_DetachRestoresShellTtyMode(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-t", "-n", "attach-tty", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "started", defaultTimeout)

	script := strings.Join([]string{
		"before=$(stty -g)",
		cmdmanBin + " attach " + id,
		"status=$?",
		"after=$(stty -g)",
		"printf 'STATUS:%s\\nBEFORE:%s\\nAFTER:%s\\n' \"$status\" \"$before\" \"$after\"",
	}, "; ")

	sh := exec.CommandContext(ctx, "/bin/sh", "-lc", script)
	sh.Env = append(
		os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
	)

	ptmx, err := pty.Start(sh)
	if err != nil {
		t.Fatalf("start shell pty: %v", err)
	}
	defer ptmx.Close()

	var output bytes.Buffer
	doneRead := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, ptmx)
		close(doneRead)
	}()

	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte{0x10, 0x11}); err != nil {
		t.Fatalf("send detach keys: %v", err)
	}

	waitAttachExit(t, sh, 3*time.Second)
	_ = ptmx.Close()
	<-doneRead

	text := output.String()
	before := extractMarkedLine(t, text, "BEFORE:")
	after := extractMarkedLine(t, text, "AFTER:")
	status := extractMarkedLine(t, text, "STATUS:")

	if status != "0" {
		t.Fatalf("shell script exited with status %q\noutput:\n%s", status, text)
	}
	if before != after {
		t.Fatalf(
			"tty mode changed across attach detach\nbefore=%q\nafter=%q\noutput:\n%s",
			before,
			after,
			text,
		)
	}
}

func TestAttach_CtrlCRestoresShellTtyMode(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-t", "-n", "attach-tty-sigint", "--", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, id, "started", defaultTimeout)

	script := strings.Join([]string{
		"before=$(stty -g)",
		cmdmanBin + " attach " + id,
		"status=$?",
		"after=$(stty -g)",
		"printf 'STATUS:%s\\nBEFORE:%s\\nAFTER:%s\\n' \"$status\" \"$before\" \"$after\"",
	}, "; ")

	sh := exec.CommandContext(ctx, "/bin/sh", "-lc", script)
	sh.Env = append(
		os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
	)

	ptmx, err := pty.Start(sh)
	if err != nil {
		t.Fatalf("start shell pty: %v", err)
	}
	defer ptmx.Close()

	var output bytes.Buffer
	doneRead := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, ptmx)
		close(doneRead)
	}()

	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte{0x03}); err != nil {
		t.Fatalf("send ctrl-c: %v", err)
	}

	waitAttachExit(t, sh, 3*time.Second)
	_ = ptmx.Close()
	<-doneRead

	text := output.String()
	before := extractMarkedLine(t, text, "BEFORE:")
	after := extractMarkedLine(t, text, "AFTER:")
	status := extractMarkedLine(t, text, "STATUS:")

	if status != "0" {
		t.Fatalf("shell script exited with status %q\noutput:\n%s", status, text)
	}
	if before != after {
		t.Fatalf(
			"tty mode changed across attach ctrl-c\nbefore=%q\nafter=%q\noutput:\n%s",
			before,
			after,
			text,
		)
	}
}

func waitAttachExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("attach exited with error: %v", err)
		}
	case <-time.After(timeout):
		t.Fatal("attach did not exit")
	}
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
	id := env.run(ctx, "run", "-t", "-n", "attach-reattach",
		"--", "/bin/sh", "-c", "sleep 0.3; echo "+marker+"; sleep 0.4")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	attach := exec.CommandContext(ctx, cmdmanBin, "attach", id)
	attach.Env = append(
		os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
	)

	ptmx, err := pty.Start(attach)
	if err != nil {
		t.Fatalf("start attach pty: %v", err)
	}
	defer ptmx.Close()

	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(b)
			if n > 0 {
				chunk := b[:n]
				mu.Lock()
				out.Write(chunk)
				mu.Unlock()
				// Answer the terminal-capability probes lipgloss/termenv emit at
				// attach startup, the same way a real terminal would. Without a
				// reply the probe blocks waiting for the \x1b[6n (DSR) response
				// and the sticky prompt never renders.
				if bytes.Contains(chunk, []byte("\x1b]11;?")) {
					_, _ = ptmx.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
				}
				if bytes.Contains(chunk, []byte("\x1b[6n")) {
					_, _ = ptmx.Write([]byte("\x1b[1;1R"))
				}
			}
			if rerr != nil {
				return
			}
		}
	}()
	snapshot := func() (string, int) {
		mu.Lock()
		defer mu.Unlock()
		return out.String(), out.Len()
	}

	// Wait for the first run to exit and the sticky restart prompt to appear.
	promptDeadline := time.Now().Add(5 * time.Second)
	for {
		s, _ := snapshot()
		if strings.Contains(s, "press 'r' to restart") {
			break
		}
		if time.Now().After(promptDeadline) {
			t.Fatalf("sticky restart prompt never appeared; output:\n%q", s)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Everything up to here (including the first run's marker) precedes mark;
	// the reattached run's output must show up after it.
	_, mark := snapshot()
	if _, err := ptmx.Write([]byte("r")); err != nil {
		t.Fatalf("send restart key: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		s, _ := snapshot()
		if len(s) >= mark && strings.Contains(s[mark:], marker) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reattach streamed no output after restart; "+
				"marker %q missing from post-restart output:\n%s", marker, s[min(mark, len(s)):])
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Detach cleanly with the default detach keys (ctrl-p, ctrl-q).
	_, _ = ptmx.Write([]byte{0x10, 0x11})
	waitAttachExit(t, attach, 5*time.Second)
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

package cmdman_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty/v2"
	"github.com/ngicks/cmdman/pkg/cmdman"
)

func TestAttach_DetachKeysExitWithoutStoppingCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "attach-detach", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

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
	env.waitForState(ctx, id, "running", defaultTimeout)
}

func TestAttach_ExitsWhenCommandStopsFromCtrlC(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "attach-sigint", "--", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, id, "running", defaultTimeout)

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

	id := env.run(ctx, "run", "-n", "attach-tty", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

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
		t.Fatalf("tty mode changed across attach detach\nbefore=%q\nafter=%q\noutput:\n%s", before, after, text)
	}
}

func TestAttach_CtrlCRestoresShellTtyMode(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "attach-tty-sigint", "--", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, id, "running", defaultTimeout)

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
		t.Fatalf("tty mode changed across attach ctrl-c\nbefore=%q\nafter=%q\noutput:\n%s", before, after, text)
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

func extractMarkedLine(t *testing.T, text, prefix string) string {
	t.Helper()

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, prefix); idx >= 0 {
			return strings.TrimPrefix(line[idx:], prefix)
		}
	}
	t.Fatalf("missing prefix %q in output:\n%s", prefix, text)
	return ""
}

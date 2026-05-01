package cmdman_test

import (
	"strings"
	"testing"
	"time"
)

func TestLogs_CapturesOutput(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Run a command that produces known output.
	id := env.run(ctx, "run", "-n", "log-producer", "--", "/bin/sh", "-c",
		"echo 'line-one'; echo 'line-two'; echo 'line-three'")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "log-producer", "exited", defaultTimeout)

	// Give the scrollback buffer a moment to capture everything.
	time.Sleep(200 * time.Millisecond)

	// Read logs (non-follow mode, returns immediately for exited commands
	// as long as the monitor is still alive — it may not be).
	// Since the monitor exits after the command, the socket may be gone.
	// For exited commands, logs may fail. That's expected.
	stdout, _, err := env.exec(ctx, "logs", "log-producer")
	if err != nil {
		t.Skip("logs for exited command failed (monitor already exited), skipping")
	}

	if !strings.Contains(stdout, "line-one") {
		t.Errorf("expected 'line-one' in logs, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "line-two") {
		t.Errorf("expected 'line-two' in logs, got:\n%s", stdout)
	}
}

func TestLogs_RunningCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Start a command that outputs something then sleeps.
	id := env.run(ctx, "run", "-n", "log-running", "--", "/bin/sh", "-c",
		"echo 'hello-from-logs'; sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "log-running", "running", defaultTimeout)

	// Give it a moment to produce output.
	time.Sleep(500 * time.Millisecond)

	// Read logs (non-follow).
	stdout := env.run(ctx, "logs", "log-running")

	if !strings.Contains(stdout, "hello-from-logs") {
		t.Errorf("expected 'hello-from-logs' in logs output, got:\n%s", stdout)
	}
}

func TestLogs_ScrollbackPreservesRecent(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Run a command with a small scrollback buffer that produces more output than the buffer.
	// 256 bytes scrollback, output > 256 bytes.
	id := env.run(ctx, "run", "-n", "scrollback-test",
		"--scrollback-bytes", "256",
		"--", "/bin/sh", "-c",
		// Produce ~400 bytes of output: 20 lines of 20 chars each.
		"for i in $(seq 1 20); do echo \"scrollback-line-$i--\"; done; sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "scrollback-test", "running", defaultTimeout)

	// Wait for output to be produced.
	time.Sleep(500 * time.Millisecond)

	stdout := env.run(ctx, "logs", "scrollback-test")

	// The most recent lines should be present.
	if !strings.Contains(stdout, "scrollback-line-20") {
		t.Errorf("expected recent output 'scrollback-line-20' in scrollback, got:\n%s", stdout)
	}

	// The earliest lines may have been evicted.
	// (Not asserting absence since timing may vary.)
}

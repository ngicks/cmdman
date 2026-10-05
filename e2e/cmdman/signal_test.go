package cmdman_test

import (
	"strings"
	"testing"
	"time"
)

func TestSignal_DoesNotDisableRestartPolicy(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "signal-restart", "--restart", "always",
		"--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "signal-restart", "running", defaultTimeout)

	env.run(ctx, "signal", "-s", "SIGTERM", "signal-restart")

	waitUntil(t, defaultTimeout, func() bool {
		info := env.inspectJSON(ctx, "signal-restart")
		history, _ := info["ExitHistory"].([]any)
		state, _ := info["State"].(string)
		return len(history) >= 1 && state == "running"
	}, "signal should allow restart policy to restart the command")
}

func TestStop_DisablesRestartPolicy(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "stop-restart", "--restart", "always",
		"--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "stop-restart", "running", defaultTimeout)

	env.run(ctx, "stop", "stop-restart")
	env.waitForState(ctx, "stop-restart", "exited", defaultTimeout)

	time.Sleep(500 * time.Millisecond)
	info := env.inspectJSON(ctx, "stop-restart")
	if state, _ := info["State"].(string); state != "exited" {
		t.Fatalf("expected exited after stop, got %v", state)
	}
}

// TestSignal_ExitStatusOnFailure pins the exit status of a signal whose target
// failed: an unknown name is a per-target failure for signal, it does not keep
// the next target from being signaled, the per-target line names the target as
// given, the aggregate names the verb, and the process exits 1.
func TestSignal_ExitStatusOnFailure(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Run(ctx, "signal-exit-running", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "signal-exit-running", "running", defaultTimeout)

	res := env.Cmd("signal", "-s", "SIGKILL", "signal-exit-missing", "signal-exit-running").
		ExpectFail(ctx, t,
			"signal signal-exit-missing:",
			"one or more signal operations failed",
		)
	if code := exitStatusOf(t, res.Err); code != 1 {
		t.Errorf("expected exit status 1, got %d", code)
	}

	env.waitForState(ctx, "signal-exit-running", "exited", defaultTimeout)
}

// TestSignal_ExitStatusIgnoreErrors is TestSignal_ExitStatusOnFailure with
// --ignore-errors: the per-target line survives, the aggregate does not, and
// the process exits 0.
func TestSignal_ExitStatusIgnoreErrors(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Run(ctx, "signal-ignore-running", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "signal-ignore-running", "running", defaultTimeout)

	res := env.Cmd(
		"signal", "-s", "SIGKILL", "--ignore-errors",
		"signal-ignore-missing", "signal-ignore-running",
	).Exec(ctx)
	if res.Err != nil {
		t.Fatalf("signal --ignore-errors failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	if want := "signal signal-ignore-missing:"; !strings.Contains(res.Stderr, want) {
		t.Errorf("expected %q in stderr, got %q", want, res.Stderr)
	}
	if strings.Contains(res.Stderr, "one or more signal operations failed") {
		t.Errorf("--ignore-errors should suppress the aggregate, got stderr=%q", res.Stderr)
	}

	env.waitForState(ctx, "signal-ignore-running", "exited", defaultTimeout)
}

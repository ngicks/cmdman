package cmdman_test

import (
	"strings"
	"testing"
)

func TestStop_RunningCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "sleeper", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.waitForState(ctx, "sleeper", "running", defaultTimeout)

	env.run(ctx, "stop", "sleeper")

	env.waitForState(ctx, "sleeper", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "sleeper")
	if info["State"] != "exited" {
		t.Errorf("expected state=exited after stop, got %v", info["State"])
	}
}

func TestStop_ByID(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.waitForState(ctx, id, "running", defaultTimeout)

	env.run(ctx, "stop", id)

	env.waitForState(ctx, id, "exited", defaultTimeout)
}

func TestStop_WithSignal(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "sig-test", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.waitForState(ctx, "sig-test", "running", defaultTimeout)

	env.run(ctx, "stop", "-s", "SIGKILL", "sig-test")

	env.waitForState(ctx, "sig-test", "exited", defaultTimeout)
}

func TestSignal_Subcommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "signal-test", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.waitForState(ctx, "signal-test", "running", defaultTimeout)
	env.run(ctx, "signal", "-s", "SIGKILL", "signal-test")
	env.waitForState(ctx, "signal-test", "exited", defaultTimeout)
}

func TestStop_AlreadyExited(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "echo done")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	// A command that already reached a terminal state is not a failed target:
	// stop is a silent no-op on it and exits 0.
	res := env.Cmd("stop", id).Exec(ctx)
	if res.Err != nil {
		t.Fatalf("stop on an exited command failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	if res.Stdout != "" || res.Stderr != "" {
		t.Errorf("expected no output, got stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}

	info := env.inspectJSON(ctx, id)
	if info["State"] != "exited" {
		t.Errorf("expected state=exited, got %v", info["State"])
	}
}

func TestStop_MultipleTargets(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id1 := env.run(ctx, "run", "--", "/bin/sh", "-c", "sleep 300")
	id2 := env.run(ctx, "run", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() {
		env.cleanupCommand(ctx, id1)
		env.cleanupCommand(ctx, id2)
	})

	env.waitForState(ctx, id1, "running", defaultTimeout)
	env.waitForState(ctx, id2, "running", defaultTimeout)

	env.run(ctx, "stop", id1, id2)

	env.waitForState(ctx, id1, "exited", defaultTimeout)
	env.waitForState(ctx, id2, "exited", defaultTimeout)
}

// TestStop_ExitStatusOnFailure pins the exit status of a stop whose target
// failed: the failure does not keep the next target from being stopped, the
// per-target line goes to stderr, the aggregate names the verb, and the process
// exits 1.
//
// The failing target is a command that was never started, so there is no
// monitor to deliver the stop to. An unknown name cannot serve: it aborts the
// whole call before any target is attempted.
func TestStop_ExitStatusOnFailure(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Create(ctx, "stop-exit-idle", "/bin/sh", "-c", "sleep 300")
	env.Run(ctx, "stop-exit-running", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "stop-exit-running", "running", defaultTimeout)

	id := env.resolvedID(ctx, "stop-exit-idle")
	res := env.Cmd("stop", "stop-exit-idle", "stop-exit-running").ExpectFail(ctx, t,
		"stop "+id+":",
		"one or more stop operations failed",
	)
	if code := exitStatusOf(t, res.Err); code != 1 {
		t.Errorf("expected exit status 1, got %d", code)
	}

	env.waitForState(ctx, "stop-exit-running", "exited", defaultTimeout)
}

// TestStop_ExitStatusIgnoreErrors is TestStop_ExitStatusOnFailure with
// --ignore-errors: the per-target line survives, the aggregate does not, and
// the process exits 0.
func TestStop_ExitStatusIgnoreErrors(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Create(ctx, "stop-ignore-idle", "/bin/sh", "-c", "sleep 300")
	env.Run(ctx, "stop-ignore-running", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "stop-ignore-running", "running", defaultTimeout)

	id := env.resolvedID(ctx, "stop-ignore-idle")
	res := env.Cmd("stop", "--ignore-errors", "stop-ignore-idle", "stop-ignore-running").Exec(ctx)
	if res.Err != nil {
		t.Fatalf("stop --ignore-errors failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	if want := "stop " + id + ":"; !strings.Contains(res.Stderr, want) {
		t.Errorf("expected %q in stderr, got %q", want, res.Stderr)
	}
	if strings.Contains(res.Stderr, "one or more stop operations failed") {
		t.Errorf("--ignore-errors should suppress the aggregate, got stderr=%q", res.Stderr)
	}

	env.waitForState(ctx, "stop-ignore-running", "exited", defaultTimeout)
}

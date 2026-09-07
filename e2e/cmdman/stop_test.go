package cmdman_test

import (
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

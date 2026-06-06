package cmdman_test

import (
	"testing"
)

func TestStop_RunningCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Start a long-running command.
	id := env.run(ctx, "run", "-n", "sleeper", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.waitForState(ctx, "sleeper", "running", defaultTimeout)

	// Stop it.
	env.run(ctx, "stop", "sleeper")

	// Wait for it to reach exited state.
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

	// Stop by ID.
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

	// Send SIGKILL explicitly.
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

	// Stopping an already-exited command prints an error per-command
	// but the stop command itself may return 0.
	// The important thing is the command remains in exited state.
	stdout, stderr, _ := env.exec(ctx, "stop", id)
	combined := stdout + " " + stderr
	// Should indicate an error (connection refused, no socket, etc.)
	if combined == " " {
		t.Log("stop on exited command produced no output (error was silent)")
	}

	// State should still be exited.
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

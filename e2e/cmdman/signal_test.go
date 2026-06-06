package cmdman_test

import (
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
		history, _ := info["exit_history"].([]any)
		state, _ := info["state"].(string)
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
	if state, _ := info["state"].(string); state != "exited" {
		t.Fatalf("expected exited after stop, got %v", state)
	}
}

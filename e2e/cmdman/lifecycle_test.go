package cmdman_test

import (
	"testing"
	"time"
)

// TestLifecycle_RunStopRm verifies the full lifecycle:
// run → verify running → stop → verify exited → rm → verify gone.
func TestLifecycle_RunStopRm(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Step 1: Run a long-lived command.
	id := env.run(ctx, "run", "-n", "lifecycle-cmd", "--", "/bin/sh", "-c", "sleep 300")

	// Step 2: Wait for running state.
	env.waitForState(ctx, "lifecycle-cmd", "running", defaultTimeout)

	// Step 3: Verify it appears in ls.
	entries := env.lsJSON(ctx)
	found := false
	for _, e := range entries {
		if e["Name"] == "lifecycle-cmd" {
			found = true
			if e["State"] != "running" {
				t.Errorf("expected state=running in ls, got %v", e["State"])
			}
		}
	}
	if !found {
		t.Fatal("lifecycle-cmd not found in ls output")
	}

	// Step 4: Inspect while running.
	info := env.inspectJSON(ctx, "lifecycle-cmd")
	if info["state"] != "running" {
		t.Errorf("expected state=running in inspect, got %v", info["state"])
	}
	liveStatus, _ := info["live_status"].(map[string]any)
	if liveStatus == nil {
		t.Error("expected live_status for running command")
	}

	// Step 5: Stop the command.
	env.run(ctx, "stop", "lifecycle-cmd")

	// Step 6: Wait for exited state.
	env.waitForState(ctx, "lifecycle-cmd", "exited", defaultTimeout)

	// Step 7: Verify exited state in inspect.
	info = env.inspectJSON(ctx, "lifecycle-cmd")
	if info["state"] != "exited" {
		t.Errorf("expected state=exited after stop, got %v", info["state"])
	}

	// Step 8: Remove.
	env.run(ctx, "rm", "lifecycle-cmd")

	// Step 9: Verify gone from ls.
	entries = env.lsJSON(ctx)
	for _, e := range entries {
		if e["ID"] == id {
			t.Error("command still found in ls after rm")
		}
	}

	// Step 10: Inspect should fail.
	_, _ = env.runExpectFail(ctx, "inspect", "lifecycle-cmd")
}

// TestLifecycle_RunAutoRemove verifies run with --rm:
// run --rm → verify running → command exits → verify auto-removed.
func TestLifecycle_RunAutoRemove(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--rm", "-n", "auto-rm-lifecycle", "--", "/bin/sh", "-c", "echo done")

	// Wait for auto-removal.
	waitUntil(t, defaultTimeout, func() bool {
		entries := env.lsJSON(ctx)
		for _, e := range entries {
			if e["ID"] == id {
				return false
			}
		}
		return true
	}, "command %s was not auto-removed", id)
}

// TestLifecycle_RunRestartStop verifies restart + stop:
// run --restart=always → verify restarts → stop → verify exited.
func TestLifecycle_RunRestartStop(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Start a command that exits immediately but always restarts.
	id := env.run(ctx, "run", "-n", "restart-lifecycle",
		"--restart", "always",
		"--", "/bin/sh", "-c", "echo restarting; exit 0")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	// Wait for multiple restarts.
	time.Sleep(2 * time.Second)

	// Verify it has restarted multiple times.
	info := env.inspectJSON(ctx, "restart-lifecycle")
	history, _ := info["exit_history"].([]any)
	if len(history) < 2 {
		t.Errorf("expected at least 2 exit_history entries, got %d", len(history))
	}

	// Stop it.
	env.run(ctx, "stop", "restart-lifecycle")
	env.waitForState(ctx, "restart-lifecycle", "exited", defaultTimeout)

	info = env.inspectJSON(ctx, "restart-lifecycle")
	if info["state"] != "exited" {
		t.Errorf("expected state=exited after stop, got %v", info["state"])
	}
}

// TestLifecycle_MultipleCommands verifies managing multiple commands simultaneously.
func TestLifecycle_MultipleCommands(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Start three commands.
	id1 := env.run(ctx, "run", "-n", "multi-1", "-l", "group=multi", "--", "/bin/sh", "-c", "sleep 300")
	id2 := env.run(ctx, "run", "-n", "multi-2", "-l", "group=multi", "--", "/bin/sh", "-c", "sleep 300")
	id3 := env.run(ctx, "run", "-n", "multi-3", "-l", "group=multi", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() {
		env.cleanupCommand(ctx, id1)
		env.cleanupCommand(ctx, id2)
		env.cleanupCommand(ctx, id3)
	})

	env.waitForState(ctx, id1, "running", defaultTimeout)
	env.waitForState(ctx, id2, "running", defaultTimeout)
	env.waitForState(ctx, id3, "running", defaultTimeout)

	// All three should appear in ls.
	entries := env.lsJSON(ctx, "-l", "group=multi")
	if len(entries) != 3 {
		t.Errorf("expected 3 entries with group=multi, got %d", len(entries))
	}

	// Stop all explicitly.
	env.run(ctx, "stop", id1, id2, id3)

	env.waitForState(ctx, id1, "exited", defaultTimeout)
	env.waitForState(ctx, id2, "exited", defaultTimeout)
	env.waitForState(ctx, id3, "exited", defaultTimeout)

	// Remove all with label.
	env.run(ctx, "rm", "-l", "group=multi")

	entries = env.lsJSON(ctx, "-l", "group=multi")
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after rm, got %d", len(entries))
	}
}

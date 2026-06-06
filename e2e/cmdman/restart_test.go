package cmdman_test

import (
	"path/filepath"
	"testing"
)

// TestRestartCmd_Running verifies `cmdman restart` on a running command: it
// stops the command, starts it again, and the new monitor is running.
func TestRestartCmd_Running(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "restart-running", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "restart-running", "running", defaultTimeout)

	before := env.inspectJSON(ctx, "restart-running")
	beforeDetail, _ := before["state_detail"].(map[string]any)
	beforePID, _ := beforeDetail["monitor_pid"].(float64)

	env.run(ctx, "restart", "restart-running")
	env.waitForState(ctx, "restart-running", "running", defaultTimeout)

	after := env.inspectJSON(ctx, "restart-running")
	afterDetail, _ := after["state_detail"].(map[string]any)
	afterPID, _ := afterDetail["monitor_pid"].(float64)

	if beforePID == afterPID {
		t.Errorf(
			"expected monitor_pid to change across restart; before=%v after=%v",
			beforePID, afterPID,
		)
	}

	history, _ := after["exit_history"].([]any)
	if len(history) < 1 {
		t.Errorf("expected at least 1 exit_history entry after restart, got %d", len(history))
	}
}

// TestRestartCmd_Exited verifies `cmdman restart` on a previously-exited
// command starts it again.
func TestRestartCmd_Exited(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "restart-exited", "--", "/bin/sh", "-c", "exit 0")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "restart-exited", "exited", defaultTimeout)

	env.run(ctx, "restart", "restart-exited")
	env.waitForState(ctx, "restart-exited", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "restart-exited")
	history, _ := info["exit_history"].([]any)
	if len(history) != 2 {
		t.Errorf("expected 2 exit_history entries after restart, got %d", len(history))
	}
}

// TestRestartCmd_Failed verifies `cmdman restart` on a failed command starts
// it again (relying on the same precondition relaxation as `cmdman start`).
func TestRestartCmd_Failed(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "later.sh")

	id := env.run(ctx, "create", "-n", "restart-failed", "--", scriptPath)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.runExpectFail(ctx, "start", "restart-failed")
	env.waitForState(ctx, "restart-failed", "failed", defaultTimeout)

	writeFile(t, scriptPath, "#!/bin/sh\nexit 0\n")

	env.run(ctx, "restart", "restart-failed")
	env.waitForState(ctx, "restart-failed", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "restart-failed")
	exitCode, _ := info["exit_code"].(float64)
	if exitCode != 0 {
		t.Errorf("expected exit_code=0 after restart from failed, got %v", exitCode)
	}
}

// TestRestartCmd_Multiple verifies `cmdman restart` accepts multiple targets
// and restarts each one.
func TestRestartCmd_Multiple(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id1 := env.run(ctx, "run", "-n", "restart-multi-1", "--", "/bin/sh", "-c", "sleep 300")
	id2 := env.run(ctx, "run", "-n", "restart-multi-2", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() {
		env.cleanupCommand(ctx, id1)
		env.cleanupCommand(ctx, id2)
	})
	env.waitForState(ctx, "restart-multi-1", "running", defaultTimeout)
	env.waitForState(ctx, "restart-multi-2", "running", defaultTimeout)

	env.run(ctx, "restart", "restart-multi-1", "restart-multi-2")
	env.waitForState(ctx, "restart-multi-1", "running", defaultTimeout)
	env.waitForState(ctx, "restart-multi-2", "running", defaultTimeout)

	for _, name := range []string{"restart-multi-1", "restart-multi-2"} {
		info := env.inspectJSON(ctx, name)
		history, _ := info["exit_history"].([]any)
		if len(history) < 1 {
			t.Errorf("%s: expected at least 1 exit_history entry after restart, got %d",
				name, len(history))
		}
	}
}

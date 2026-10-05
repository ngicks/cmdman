package cmdman_test

import (
	"path/filepath"
	"strings"
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
	beforeDetail, _ := before["StateJSON"].(map[string]any)
	beforePID, _ := beforeDetail["monitor_pid"].(float64)

	env.run(ctx, "restart", "restart-running")
	env.waitForState(ctx, "restart-running", "running", defaultTimeout)

	after := env.inspectJSON(ctx, "restart-running")
	afterDetail, _ := after["StateJSON"].(map[string]any)
	afterPID, _ := afterDetail["monitor_pid"].(float64)

	if beforePID == afterPID {
		t.Errorf(
			"expected monitor_pid to change across restart; before=%v after=%v",
			beforePID, afterPID,
		)
	}

	history, _ := after["ExitHistory"].([]any)
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
	history, _ := info["ExitHistory"].([]any)
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
	exitCode, _ := info["ExitCode"].(float64)
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
		history, _ := info["ExitHistory"].([]any)
		if len(history) < 1 {
			t.Errorf("%s: expected at least 1 exit_history entry after restart, got %d",
				name, len(history))
		}
	}
}

// TestRestartCmd_ExitStatusOnFailure pins the exit status of a restart whose
// target failed: the failure does not keep the next target from being
// restarted, the per-target line goes to stderr, the aggregate names the verb,
// and the process exits 1.
//
// The failing target is a command whose executable does not exist, so its start
// half fails. An unknown name cannot serve: it aborts the whole call before any
// target is attempted.
func TestRestartCmd_ExitStatusOnFailure(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Create(ctx, "restart-exit-broken", filepath.Join(t.TempDir(), "missing.sh"))
	env.Run(ctx, "restart-exit-running", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "restart-exit-running", "running", defaultTimeout)

	id := env.resolvedID(ctx, "restart-exit-broken")
	res := env.Cmd("restart", "restart-exit-broken", "restart-exit-running").ExpectFail(ctx, t,
		"restart "+id+": start:",
		"one or more restart operations failed",
	)
	if code := exitStatusOf(t, res.Err); code != 1 {
		t.Errorf("expected exit status 1, got %d", code)
	}

	env.waitForState(ctx, "restart-exit-running", "running", defaultTimeout)
	info := env.inspectJSON(ctx, "restart-exit-running")
	history, _ := info["ExitHistory"].([]any)
	if len(history) < 1 {
		t.Errorf("expected at least 1 exit_history entry after restart, got %d", len(history))
	}
}

// TestRestartCmd_ExitStatusIgnoreErrors is TestRestartCmd_ExitStatusOnFailure
// with --ignore-errors: the per-target line survives, the aggregate does not,
// and the process exits 0.
func TestRestartCmd_ExitStatusIgnoreErrors(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Create(ctx, "restart-ignore-broken", filepath.Join(t.TempDir(), "missing.sh"))
	env.Run(ctx, "restart-ignore-running", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "restart-ignore-running", "running", defaultTimeout)

	id := env.resolvedID(ctx, "restart-ignore-broken")
	res := env.Cmd(
		"restart", "--ignore-errors", "restart-ignore-broken", "restart-ignore-running",
	).Exec(ctx)
	if res.Err != nil {
		t.Fatalf("restart --ignore-errors failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	if want := "restart " + id + ": start:"; !strings.Contains(res.Stderr, want) {
		t.Errorf("expected %q in stderr, got %q", want, res.Stderr)
	}
	if strings.Contains(res.Stderr, "one or more restart operations failed") {
		t.Errorf("--ignore-errors should suppress the aggregate, got stderr=%q", res.Stderr)
	}

	env.waitForState(ctx, "restart-ignore-running", "running", defaultTimeout)
	info := env.inspectJSON(ctx, "restart-ignore-running")
	history, _ := info["ExitHistory"].([]any)
	if len(history) < 1 {
		t.Errorf("expected at least 1 exit_history entry after restart, got %d", len(history))
	}
}

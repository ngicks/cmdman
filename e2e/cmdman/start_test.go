package cmdman_test

import (
	"path/filepath"
	"testing"
)

// TestStart_FromFailedState verifies that `cmdman start` accepts a command
// whose previous run left it in the "failed" state (e.g. the binary did not
// exist), and that re-running with a now-valid binary succeeds.
func TestStart_FromFailedState(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Point the command at a binary path that does not yet exist so the
	// monitor will set state=failed when it tries to exec it.
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "later.sh")

	id := env.run(ctx, "create", "-n", "start-failed", "--", scriptPath)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	// First start must fail: binary does not exist.
	env.runExpectFail(ctx, "start", "start-failed")
	env.waitForState(ctx, "start-failed", "failed", defaultTimeout)

	// Materialise the binary so the next start can succeed.
	writeFile(t, scriptPath, "#!/bin/sh\nexit 0\n")

	// Starting from "failed" should now be allowed and the command should
	// reach "exited" with code 0.
	env.run(ctx, "start", "start-failed")
	env.waitForState(ctx, "start-failed", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "start-failed")
	if info["state"] != "exited" {
		t.Errorf("expected state=exited after restart from failed, got %v", info["state"])
	}
	exitCode, _ := info["exit_code"].(float64)
	if exitCode != 0 {
		t.Errorf("expected exit_code=0 after restart from failed, got %v", exitCode)
	}
}

// TestStart_FromFailedState_StillBroken verifies that re-starting a failed
// command whose binary is still missing reports the failure rather than
// confusing the leftover "failed" state with a new one.
func TestStart_FromFailedState_StillBroken(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "missing.sh")

	id := env.run(ctx, "create", "-n", "start-failed-twice", "--", scriptPath)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.runExpectFail(ctx, "start", "start-failed-twice")
	env.waitForState(ctx, "start-failed-twice", "failed", defaultTimeout)

	// Second start must also fail (binary still missing) and must surface
	// the failure to the caller instead of returning success because the
	// state happened to match the previous "failed".
	env.runExpectFail(ctx, "start", "start-failed-twice")
	env.waitForState(ctx, "start-failed-twice", "failed", defaultTimeout)
}

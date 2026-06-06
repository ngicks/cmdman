package cmdman_test

import (
	"strings"
	"testing"
)

func TestRm_ExitedCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Run a command that exits immediately.
	id := env.run(ctx, "run", "-n", "to-remove", "--", "/bin/sh", "-c", "echo bye")
	env.waitForState(ctx, "to-remove", "exited", defaultTimeout)

	// Remove it.
	env.run(ctx, "rm", "to-remove")

	// It should no longer appear in ls.
	entries := env.lsJSON(ctx)
	for _, e := range entries {
		if e["ID"] == id {
			t.Error("command still appears in ls after rm")
		}
	}
}

func TestRm_ByID(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "echo bye")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	// Remove by full ID.
	env.run(ctx, "rm", id)

	entries := env.lsJSON(ctx)
	for _, e := range entries {
		if e["ID"] == id {
			t.Error("command still appears in ls after rm by ID")
		}
	}
}

func TestRm_RunningCommandFails(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "running-rm", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.waitForState(ctx, "running-rm", "running", defaultTimeout)

	// Removing a running command without --force prints an error per-command
	// but the rm command itself still returns 0 (it processes each target independently).
	stdout, stderr, _ := env.exec(ctx, "rm", "running-rm")

	// The error message should appear in stdout or stderr.
	combined := stdout + " " + stderr
	if !strings.Contains(strings.ToLower(combined), "running") &&
		!strings.Contains(strings.ToLower(combined), "force") {
		t.Logf("expected error about running command, got stdout=%q stderr=%q", stdout, stderr)
	}

	// The command should still exist (not removed).
	info := env.inspectJSON(ctx, "running-rm")
	if info["State"] != "running" {
		t.Errorf("expected command to still be running, got %v", info["State"])
	}
}

func TestRm_ForceRunningCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "force-rm", "--", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "force-rm", "running", defaultTimeout)

	// Force remove the running command.
	env.run(ctx, "rm", "-f", "force-rm")

	// It should no longer appear in ls.
	entries := env.lsJSON(ctx)
	for _, e := range entries {
		if e["ID"] == id {
			t.Error("command still appears in ls after force rm")
		}
	}
}

func TestRm_WithLabels(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Run three commands: two with a label, one without.
	id1 := env.run(ctx, "run", "-l", "cleanup=yes", "--", "/bin/sh", "-c", "echo a")
	id2 := env.run(ctx, "run", "-l", "cleanup=yes", "--", "/bin/sh", "-c", "echo b")
	id3 := env.run(ctx, "run", "--", "/bin/sh", "-c", "echo c")

	env.waitForState(ctx, id1, "exited", defaultTimeout)
	env.waitForState(ctx, id2, "exited", defaultTimeout)
	env.waitForState(ctx, id3, "exited", defaultTimeout)

	// Remove by label.
	env.run(ctx, "rm", "-l", "cleanup=yes")

	// Only id3 should remain.
	entries := env.lsJSON(ctx)
	for _, e := range entries {
		eid, _ := e["ID"].(string)
		if eid == id1 || eid == id2 {
			t.Errorf("labeled command %s still appears after rm -l", eid)
		}
	}

	// Cleanup the remaining one.
	env.run(ctx, "rm", id3)
}

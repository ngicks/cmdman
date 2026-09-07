package cmdman_test

import (
	"context"
	"strings"
	"testing"
)

func TestRm_ExitedCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "to-remove", "--", "/bin/sh", "-c", "echo bye")
	env.waitForState(ctx, "to-remove", "exited", defaultTimeout)

	env.run(ctx, "rm", "to-remove")

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

	env.run(ctx, "run", "-n", "running-rm", "--", "/bin/sh", "-c", "sleep 300")
	// Not ctx: cleanup runs after the test's context is already cancelled.
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "running-rm") })

	env.waitForState(ctx, "running-rm", "running", defaultTimeout)

	// Removing a running command without --force refuses that one target: the
	// refusal is printed per command and the process exits non-zero.
	id := env.resolvedID(ctx, "running-rm")
	res := env.Cmd("rm", "running-rm").ExpectFail(ctx, t, "rm "+id+":")
	if !strings.Contains(strings.ToLower(res.Stderr), "force") {
		t.Errorf("expected the refusal to point at --force, got stderr=%q", res.Stderr)
	}

	info := env.inspectJSON(ctx, "running-rm")
	if info["State"] != "running" {
		t.Errorf("expected command to still be running, got %v", info["State"])
	}
}

// TestRm_ExitStatusOnFailure pins the exit status of a multi-target verb whose
// target failed: the per-target line goes to stderr, the aggregate names the
// verb, and the process exits non-zero so a "rm … && …" chain stops there.
func TestRm_ExitStatusOnFailure(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "rm-exit-status", "--", "/bin/sh", "-c", "sleep 300")
	// Not ctx: cleanup runs after the test's context is already cancelled.
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "rm-exit-status") })

	env.waitForState(ctx, "rm-exit-status", "running", defaultTimeout)

	id := env.resolvedID(ctx, "rm-exit-status")
	env.Cmd("rm", "rm-exit-status").ExpectFail(ctx, t,
		"rm "+id+": command is running, use --force to remove",
		"one or more rm operations failed",
	)

	env.run(ctx, "stop", "rm-exit-status")
	env.waitForState(ctx, "rm-exit-status", "exited", defaultTimeout)
	env.run(ctx, "rm", "rm-exit-status")
}

// TestRm_ExitStatusIgnoreErrors is TestRm_ExitStatusOnFailure with
// --ignore-errors: the per-target line survives, the aggregate does not, and
// the process exits 0.
func TestRm_ExitStatusIgnoreErrors(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "rm-ignore-errors", "--", "/bin/sh", "-c", "sleep 300")
	// Not ctx: cleanup runs after the test's context is already cancelled.
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "rm-ignore-errors") })

	env.waitForState(ctx, "rm-ignore-errors", "running", defaultTimeout)

	id := env.resolvedID(ctx, "rm-ignore-errors")
	res := env.Cmd("rm", "--ignore-errors", "rm-ignore-errors").Exec(ctx)
	if res.Err != nil {
		t.Fatalf("rm --ignore-errors failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	if want := "rm " + id + ":"; !strings.Contains(res.Stderr, want) {
		t.Errorf("expected %q in stderr, got %q", want, res.Stderr)
	}
	if strings.Contains(res.Stderr, "one or more rm operations failed") {
		t.Errorf("--ignore-errors should suppress the aggregate, got stderr=%q", res.Stderr)
	}

	env.run(ctx, "stop", "rm-ignore-errors")
	env.waitForState(ctx, "rm-ignore-errors", "exited", defaultTimeout)
	env.run(ctx, "rm", "rm-ignore-errors")
}

func TestRm_ForceRunningCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "force-rm", "--", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "force-rm", "running", defaultTimeout)

	env.run(ctx, "rm", "-f", "force-rm")

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

	id1 := env.run(ctx, "run", "-l", "cleanup=yes", "--", "/bin/sh", "-c", "echo a")
	id2 := env.run(ctx, "run", "-l", "cleanup=yes", "--", "/bin/sh", "-c", "echo b")
	id3 := env.run(ctx, "run", "--", "/bin/sh", "-c", "echo c")

	env.waitForState(ctx, id1, "exited", defaultTimeout)
	env.waitForState(ctx, id2, "exited", defaultTimeout)
	env.waitForState(ctx, id3, "exited", defaultTimeout)

	env.run(ctx, "rm", "-l", "cleanup=yes")

	entries := env.lsJSON(ctx)
	for _, e := range entries {
		eid, _ := e["ID"].(string)
		if eid == id1 || eid == id2 {
			t.Errorf("labeled command %s still appears after rm -l", eid)
		}
	}

	env.run(ctx, "rm", id3)
}

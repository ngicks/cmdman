package cmdman_test

import (
	"strings"
	"testing"
	"time"
)

func TestWait_AlreadyExited(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 0")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	stdout := env.run(ctx, "wait", id)
	if stdout != "0" {
		t.Errorf("expected wait stdout %q, got %q", "0", stdout)
	}
}

func TestWait_NonZeroExit(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 42")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	stdout := env.run(ctx, "wait", id)
	if stdout != "42" {
		t.Errorf("expected wait stdout %q, got %q", "42", stdout)
	}
}

func TestWait_BlocksUntilExit(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "wait-target", "--", "/bin/sh", "-c", "sleep 1; exit 7")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	env.waitForState(ctx, "wait-target", "running", defaultTimeout)

	start := time.Now()
	stdout := env.run(ctx, "wait", "wait-target")
	elapsed := time.Since(start)

	if stdout != "7" {
		t.Errorf("expected wait stdout %q, got %q", "7", stdout)
	}
	if elapsed < 500*time.Millisecond {
		t.Errorf("wait returned in %v, expected to block until command exits", elapsed)
	}
}

func TestWait_MultipleTargets(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id1 := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 1")
	id2 := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 2")
	env.waitForState(ctx, id1, "exited", defaultTimeout)
	env.waitForState(ctx, id2, "exited", defaultTimeout)

	stdout := env.run(ctx, "wait", id1, id2)
	got := strings.Split(stdout, "\n")
	want := []string{"1", "2"}
	if len(got) != len(want) {
		t.Fatalf("expected %d lines, got %d (%q)", len(want), len(got), stdout)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestWait_MissingTarget(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	stdout, stderr := env.runExpectFail(ctx, "wait", "no-such-command")
	if stdout != "" {
		t.Errorf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "no-such-command") {
		t.Errorf("expected stderr to mention missing target, got %q", stderr)
	}
}

func TestWait_IgnoreMissingTarget(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 5")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	stdout := env.run(ctx, "wait", "--ignore", "no-such-command", id)
	if stdout != "5" {
		t.Errorf("expected stdout %q, got %q", "5", stdout)
	}
}

func TestWait_ConditionRunning(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "wait-running", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	stdout := env.run(ctx, "wait", "-c", "running", "wait-running")
	if stdout != "0" {
		t.Errorf("expected stdout %q for running condition, got %q", "0", stdout)
	}
}

func TestWait_InvalidCondition(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 0")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	_, stderr := env.runExpectFail(ctx, "wait", "-c", "bogus", id)
	if !strings.Contains(stderr, "invalid wait condition") {
		t.Errorf("expected stderr to mention invalid condition, got %q", stderr)
	}
}

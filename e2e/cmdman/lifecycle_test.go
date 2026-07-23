package cmdman_test

import (
	"testing"
	"time"
)

func TestLifecycle_RunStopRm(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "lifecycle-cmd", "--", "/bin/sh", "-c", "sleep 300")

	env.waitForState(ctx, "lifecycle-cmd", "running", defaultTimeout)

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

	info := env.inspectJSON(ctx, "lifecycle-cmd")
	if info["State"] != "running" {
		t.Errorf("expected state=running in inspect, got %v", info["State"])
	}
	liveStatus, _ := info["LiveStatus"].(map[string]any)
	if liveStatus == nil {
		t.Error("expected live_status for running command")
	}

	env.run(ctx, "stop", "lifecycle-cmd")

	env.waitForState(ctx, "lifecycle-cmd", "exited", defaultTimeout)

	info = env.inspectJSON(ctx, "lifecycle-cmd")
	if info["State"] != "exited" {
		t.Errorf("expected state=exited after stop, got %v", info["State"])
	}

	env.run(ctx, "rm", "lifecycle-cmd")

	entries = env.lsJSON(ctx)
	for _, e := range entries {
		if e["ID"] == id {
			t.Error("command still found in ls after rm")
		}
	}

	_, _ = env.runExpectFail(ctx, "inspect", "lifecycle-cmd")
}

func TestLifecycle_RunAutoRemove(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--rm", "-n", "auto-rm-lifecycle", "--", "/bin/sh", "-c", "echo done")

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

func TestLifecycle_RunRestartStop(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "restart-lifecycle",
		"--restart", "always",
		"--", "/bin/sh", "-c", "echo restarting; exit 0")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	time.Sleep(2 * time.Second)

	info := env.inspectJSON(ctx, "restart-lifecycle")
	history, _ := info["ExitHistory"].([]any)
	if len(history) < 2 {
		t.Errorf("expected at least 2 exit_history entries, got %d", len(history))
	}

	env.run(ctx, "stop", "restart-lifecycle")
	env.waitForState(ctx, "restart-lifecycle", "exited", defaultTimeout)

	info = env.inspectJSON(ctx, "restart-lifecycle")
	if info["State"] != "exited" {
		t.Errorf("expected state=exited after stop, got %v", info["State"])
	}
}

func TestLifecycle_MultipleCommands(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id1 := env.run(
		ctx,
		"run",
		"-n",
		"multi-1",
		"-l",
		"group=multi",
		"--",
		"/bin/sh",
		"-c",
		"sleep 300",
	)
	id2 := env.run(
		ctx,
		"run",
		"-n",
		"multi-2",
		"-l",
		"group=multi",
		"--",
		"/bin/sh",
		"-c",
		"sleep 300",
	)
	id3 := env.run(
		ctx,
		"run",
		"-n",
		"multi-3",
		"-l",
		"group=multi",
		"--",
		"/bin/sh",
		"-c",
		"sleep 300",
	)
	t.Cleanup(func() {
		env.cleanupCommand(ctx, id1)
		env.cleanupCommand(ctx, id2)
		env.cleanupCommand(ctx, id3)
	})

	env.waitForState(ctx, id1, "running", defaultTimeout)
	env.waitForState(ctx, id2, "running", defaultTimeout)
	env.waitForState(ctx, id3, "running", defaultTimeout)

	entries := env.lsJSON(ctx, "-l", "group=multi")
	if len(entries) != 3 {
		t.Errorf("expected 3 entries with group=multi, got %d", len(entries))
	}

	env.run(ctx, "stop", id1, id2, id3)

	env.waitForState(ctx, id1, "exited", defaultTimeout)
	env.waitForState(ctx, id2, "exited", defaultTimeout)
	env.waitForState(ctx, id3, "exited", defaultTimeout)

	env.run(ctx, "rm", "-l", "group=multi")

	entries = env.lsJSON(ctx, "-l", "group=multi")
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after rm, got %d", len(entries))
	}
}

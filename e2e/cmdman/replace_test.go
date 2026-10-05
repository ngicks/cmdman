package cmdman_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// inspectArgv returns the stored argv of a command.
func (e *testEnv) inspectArgv(ctx context.Context, idOrName string) []string {
	e.t.Helper()
	cfg, _ := e.inspectJSON(ctx, idOrName)["Config"].(map[string]any)
	raw, _ := cfg["argv"].([]any)
	argv := make([]string, 0, len(raw))
	for _, a := range raw {
		s, _ := a.(string)
		argv = append(argv, s)
	}
	return argv
}

// countNamed counts the commands ls lists under name.
func (e *testEnv) countNamed(ctx context.Context, name string) int {
	e.t.Helper()
	n := 0
	for _, entry := range e.lsJSON(ctx) {
		if entry["Name"] == name {
			n++
		}
	}
	return n
}

func TestCreate_Replace(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Create(ctx, "x", "echo", "a")
	oldID := env.resolvedID(ctx, "x")

	stdout := env.run(ctx, "create", "--replace", "-n", "x", "--", "echo", "b")
	if stdout != "x" {
		t.Errorf("expected create --replace to print %q, got %q", "x", stdout)
	}

	if n := env.countNamed(ctx, "x"); n != 1 {
		t.Fatalf("expected exactly one command named x, got %d", n)
	}
	newID := env.resolvedID(ctx, "x")
	if newID == oldID {
		t.Errorf("expected the replacement to get a new ID, still %s", oldID)
	}
	if argv := env.inspectArgv(ctx, "x"); !slices.Equal(argv, []string{"echo", "b"}) {
		t.Errorf("expected argv [echo b], got %v", argv)
	}
	if info := env.inspectJSON(ctx, "x"); info["State"] != "created" {
		t.Errorf("expected state=created, got %v", info["State"])
	}

	oldDir := filepath.Join(env.dataHome, "commands", oldID)
	if _, err := os.Stat(oldDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected old command dir %s to be removed, got err=%v", oldDir, err)
	}
	newConfig := filepath.Join(env.dataHome, "commands", newID, "config.json")
	if _, err := os.Stat(newConfig); err != nil {
		t.Errorf("expected new config at %s, got err=%v", newConfig, err)
	}

	oldEvents := collectEventTypes(t, env.run(ctx, "events", "--no-follow", "--id", oldID))
	if _, ok := oldEvents["removed"]; !ok {
		t.Errorf("expected a removed event for %s, got %v", oldID, sortedKeys(oldEvents))
	}
	newEvents := collectEventTypes(t, env.run(ctx, "events", "--no-follow", "--id", newID))
	if _, ok := newEvents["created"]; !ok {
		t.Errorf("expected a created event for %s, got %v", newID, sortedKeys(newEvents))
	}
}

func TestCreate_ReplaceWithoutExistingCreates(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "create", "--replace", "-n", "fresh", "--", "echo", "a")
	// Not ctx: cleanup runs after the test's context is already cancelled.
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "fresh") })

	if argv := env.inspectArgv(ctx, "fresh"); !slices.Equal(argv, []string{"echo", "a"}) {
		t.Errorf("expected argv [echo a], got %v", argv)
	}
}

func TestCreate_ReplaceRunningFails(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Run(ctx, "x", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "x", "running", defaultTimeout)
	oldID := env.resolvedID(ctx, "x")

	env.Cmd("create", "--replace", "-n", "x", "--", "echo", "b").
		ExpectFail(ctx, t, `"x"`, "running")

	info := env.inspectJSON(ctx, "x")
	if info["State"] != "running" {
		t.Errorf("expected x to keep running, got %v", info["State"])
	}
	if info["ID"] != oldID {
		t.Errorf("expected x to keep ID %s, got %v", oldID, info["ID"])
	}
	want := []string{"/bin/sh", "-c", "sleep 300"}
	if argv := env.inspectArgv(ctx, "x"); !slices.Equal(argv, want) {
		t.Errorf("expected argv %v, got %v", want, argv)
	}
}

func TestCreate_ReplaceInvalidKeepsOld(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Create(ctx, "x", "echo", "a")
	oldID := env.resolvedID(ctx, "x")

	// A negative scrollback limit passes flag parsing and is rejected by the
	// service's config validation.
	env.Cmd("create", "--replace", "-n", "x", "--scrollback-bytes=-1", "--", "echo", "b").
		ExpectFail(ctx, t, "command config:")

	if n := env.countNamed(ctx, "x"); n != 1 {
		t.Fatalf("expected exactly one command named x, got %d", n)
	}
	if id := env.resolvedID(ctx, "x"); id != oldID {
		t.Errorf("expected x to keep ID %s, got %s", oldID, id)
	}
	if argv := env.inspectArgv(ctx, "x"); !slices.Equal(argv, []string{"echo", "a"}) {
		t.Errorf("expected argv [echo a], got %v", argv)
	}
	oldConfig := filepath.Join(env.dataHome, "commands", oldID, "config.json")
	if _, err := os.Stat(oldConfig); err != nil {
		t.Errorf("expected old config at %s to remain, got err=%v", oldConfig, err)
	}
}

func TestRun_Replace(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Run(ctx, "x", "/bin/sh", "-c", "echo a")
	env.waitForState(ctx, "x", "exited", defaultTimeout)
	oldID := env.resolvedID(ctx, "x")

	stdout := env.run(ctx, "run", "--replace", "-n", "x", "--", "/bin/sh", "-c", "sleep 300")
	if stdout != "x" {
		t.Errorf("expected run --replace to print %q, got %q", "x", stdout)
	}
	env.waitForState(ctx, "x", "running", defaultTimeout)

	if n := env.countNamed(ctx, "x"); n != 1 {
		t.Fatalf("expected exactly one command named x, got %d", n)
	}
	if id := env.resolvedID(ctx, "x"); id == oldID {
		t.Errorf("expected the replacement to get a new ID, still %s", oldID)
	}
	want := []string{"/bin/sh", "-c", "sleep 300"}
	if argv := env.inspectArgv(ctx, "x"); !slices.Equal(argv, want) {
		t.Errorf("expected argv %v, got %v", want, argv)
	}
}

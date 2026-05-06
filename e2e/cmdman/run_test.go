package cmdman_test

import (
	"context"
	"testing"
	"time"
)

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestRun_BasicCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Run a command that exits immediately.
	stdout := env.run(ctx, "run", "--", "/bin/sh", "-c", "echo hello")

	// stdout should contain the command ID (32-char hex string).
	id := stdout
	if len(id) != 32 {
		t.Fatalf("expected 32-char hex ID in output, got %q (len=%d)", id, len(id))
	}

	// Wait for it to exit.
	env.waitForState(ctx, id, "exited", defaultTimeout)

	// Verify the command exited with code 0.
	info := env.inspectJSON(ctx, id)
	if info["state"] != "exited" {
		t.Errorf("expected state=exited, got %v", info["state"])
	}
	exitCode, _ := info["exit_code"].(float64)
	if exitCode != 0 {
		t.Errorf("expected exit_code=0, got %v", exitCode)
	}
}

func TestRun_WithName(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Run with a human-readable name.
	stdout := env.run(ctx, "run", "-n", "my-echo", "--", "/bin/sh", "-c", "echo named")

	// stdout should be the name, not the ID.
	if stdout != "my-echo" {
		t.Errorf("expected name %q in output, got %q", "my-echo", stdout)
	}

	env.waitForState(ctx, "my-echo", "exited", defaultTimeout)

	// Inspect by name.
	info := env.inspectJSON(ctx, "my-echo")
	if info["name"] != "my-echo" {
		t.Errorf("expected name=my-echo, got %v", info["name"])
	}
}

func TestRun_NonZeroExitCode(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 42")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	exitCode, _ := info["exit_code"].(float64)
	if exitCode != 42 {
		t.Errorf("expected exit_code=42, got %v", exitCode)
	}
}

func TestRun_WithWorkingDirectory(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-C", "/tmp", "--", "/bin/sh", "-c", "pwd")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	cfg, _ := info["config"].(map[string]any)
	if cfg["dir"] != "/tmp" {
		t.Errorf("expected dir=/tmp, got %v", cfg["dir"])
	}
}

func TestRun_WithEnvVars(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run",
		"-E", "MY_VAR=hello",
		"-E", "OTHER_VAR=world",
		"--", "/bin/sh", "-c", "echo $MY_VAR $OTHER_VAR",
	)
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	cfg, _ := info["config"].(map[string]any)
	envList, _ := cfg["env"].([]any)

	found := map[string]bool{}
	for _, e := range envList {
		s, _ := e.(string)
		if s == "MY_VAR=hello" {
			found["MY_VAR"] = true
		}
		if s == "OTHER_VAR=world" {
			found["OTHER_VAR"] = true
		}
	}
	if !found["MY_VAR"] || !found["OTHER_VAR"] {
		t.Errorf("expected MY_VAR and OTHER_VAR in env, got %v", envList)
	}
}

func TestRun_AutoRemove(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--rm", "--", "/bin/sh", "-c", "echo ephemeral")

	// Wait for auto-removal. The command should disappear from ls.
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

func TestRun_DuplicateName(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Run a long-lived command with a name.
	env.run(ctx, "run", "-n", "unique-name", "--", "/bin/sh", "-c", "sleep 60")
	t.Cleanup(func() { env.cleanupCommand(ctx, "unique-name") })

	env.waitForState(ctx, "unique-name", "running", defaultTimeout)

	// Running another command with the same name should fail.
	stdout, stderr, err := env.exec(
		ctx,
		"run",
		"-n",
		"unique-name",
		"--",
		"/bin/sh",
		"-c",
		"echo duplicate",
	)
	if err == nil {
		t.Logf("expected error for duplicate name, got stdout=%q stderr=%q", stdout, stderr)
		t.Fatal("run with duplicate name should fail")
	}
}

func TestRun_ScrollbackBytesFlag(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--scrollback-bytes", "2048", "--", "/bin/sh", "-c", "echo hi")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	cfg, _ := info["config"].(map[string]any)
	scrollback, _ := cfg["scrollback_bytes"].(float64)
	if scrollback != 2048 {
		t.Errorf("expected scrollback_bytes=2048, got %v", scrollback)
	}
}

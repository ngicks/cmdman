package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman"
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

	stdout := env.run(ctx, "run", "--", "/bin/sh", "-c", "echo hello")

	id := stdout
	if len(id) != 32 {
		t.Fatalf("expected 32-char hex ID in output, got %q (len=%d)", id, len(id))
	}

	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	if info["State"] != "exited" {
		t.Errorf("expected state=exited, got %v", info["State"])
	}
	exitCode, _ := info["ExitCode"].(float64)
	if exitCode != 0 {
		t.Errorf("expected exit_code=0, got %v", exitCode)
	}
}

func TestRun_WithName(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	stdout := env.run(ctx, "run", "-n", "my-echo", "--", "/bin/sh", "-c", "echo named")

	if stdout != "my-echo" {
		t.Errorf("expected name %q in output, got %q", "my-echo", stdout)
	}

	env.waitForState(ctx, "my-echo", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "my-echo")
	if info["Name"] != "my-echo" {
		t.Errorf("expected name=my-echo, got %v", info["Name"])
	}
}

func TestRun_NonZeroExitCode(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 42")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	exitCode, _ := info["ExitCode"].(float64)
	if exitCode != 42 {
		t.Errorf("expected exit_code=42, got %v", exitCode)
	}
}

// TestRun_ExitWithSurvivors pins the end of a run whose command leaves a
// process behind on the output pipes. The run ends when the command is reaped
// and the survivor is swept, so the state reaches exited with the command's own
// status instead of waiting out a drain the survivor keeps open and failing.
func TestRun_ExitWithSurvivors(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	pidFile := filepath.Join(t.TempDir(), "survivor.pid")
	env.run(ctx, "run", "-n", "orphaner", "--", "/bin/sh", "-c",
		fmt.Sprintf("echo hello; sleep 300 & echo $! > %s; exit 0", pidFile))
	// Not ctx: cleanup runs after the test's context is already cancelled.
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "orphaner") })

	survivor := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(survivor) })

	start := time.Now()
	env.waitForState(ctx, "orphaner", "exited", defaultTimeout)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the run took %s to end; the survivor held the output pipes", elapsed)
	}

	info := env.inspectJSON(ctx, "orphaner")
	if exitCode, _ := info["ExitCode"].(float64); exitCode != 0 {
		t.Errorf("expected exit_code=0, got %v", exitCode)
	}

	if out := env.run(ctx, "logs", "orphaner"); !strings.Contains(out, "hello") {
		t.Errorf("expected the command's output in the logs, got %q", out)
	}

	waitUntil(t, 5*time.Second, func() bool { return !processExists(survivor) },
		"survivor pid %d is still in /proc; the run did not sweep it", survivor)
}

func TestRun_WithWorkingDirectory(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--workdir", "/tmp", "--", "/bin/sh", "-c", "pwd")
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	cfg, _ := info["Config"].(map[string]any)
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
	cfg, _ := info["Config"].(map[string]any)
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

// configEnv returns the resolved Config.env slice for a command as []string.
func (e *testEnv) configEnv(ctx context.Context, idOrName string) []string {
	e.t.Helper()
	info := e.inspectJSON(ctx, idOrName)
	cfg, _ := info["Config"].(map[string]any)
	raw, _ := cfg["env"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// TestRun_ImportsHostEnvByDefault verifies the default behavior: the host
// environment is imported into the command even when --env is also given. This
// is the post-v0.0.14 behavior — previously any explicit --env suppressed the
// host environment entirely.
func TestRun_ImportsHostEnvByDefault(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// CMDMAN_E2E_HOST_VAR is present in the cmdman process environment, so it is
	// part of the host environment the command should inherit.
	stdout, stderr, err := env.execWithExtraEnv(ctx,
		[]string{"CMDMAN_E2E_HOST_VAR=from-host"},
		"run", "-E", "MY_VAR=explicit", "--", "/bin/sh", "-c", "true",
	)
	if err != nil {
		t.Fatalf("run failed: %v\nstderr:\n%s", err, stderr)
	}
	id := stdout
	env.waitForState(ctx, id, "exited", defaultTimeout)

	cfgEnv := env.configEnv(ctx, id)
	if !slices.Contains(cfgEnv, "CMDMAN_E2E_HOST_VAR=from-host") {
		t.Errorf("expected host env var imported alongside --env, got %v", cfgEnv)
	}
	if !slices.Contains(cfgEnv, "MY_VAR=explicit") {
		t.Errorf("expected explicit --env entry present, got %v", cfgEnv)
	}
}

// TestRun_ImportHostEnvDisabled verifies that --import-host-env=false starts the
// command from an empty environment plus the explicit --env entries (the host
// environment is not imported).
func TestRun_ImportHostEnvDisabled(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	stdout, stderr, err := env.execWithExtraEnv(ctx,
		[]string{"CMDMAN_E2E_HOST_VAR=from-host"},
		"run", "--import-host-env=false", "-E", "MY_VAR=explicit",
		"--", "/bin/sh", "-c", "true",
	)
	if err != nil {
		t.Fatalf("run failed: %v\nstderr:\n%s", err, stderr)
	}
	id := stdout
	env.waitForState(ctx, id, "exited", defaultTimeout)

	cfgEnv := env.configEnv(ctx, id)
	if envHasKey(cfgEnv, "CMDMAN_E2E_HOST_VAR") {
		t.Errorf("expected host env var to be excluded, got %v", cfgEnv)
	}
	if !slices.Contains(cfgEnv, "MY_VAR=explicit") {
		t.Errorf("expected explicit --env entry present, got %v", cfgEnv)
	}
}

func TestRun_InjectsCmdmanContextEnv(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx,
		"run",
		"-E", cmdman.ENV_CMDMAN_DATA_DIR+"=/wrong",
		"-E", cmdman.ENV_CMDMAN_RUNTIME_DIR+"=/wrong",
		"-E", cmdman.ENV_CMDMAN_CMD_DATA_DIR+"=/wrong",
		"-E", cmdman.ENV_CMDMAN_CMD_ID+"=wrong",
		"-E", "EXPECT_DATA="+env.dataHome,
		"-E", "EXPECT_RUNTIME="+env.runtimeDir,
		"--",
		"/bin/sh", "-c", `
test "$CMDMAN_DATA_DIR" = "$EXPECT_DATA" &&
test "$CMDMAN_RUNTIME_DIR" = "$EXPECT_RUNTIME" &&
test "$CMDMAN_CMD_DATA_DIR" = "$CMDMAN_DATA_DIR/commands/$CMDMAN_CMD_ID" &&
test -f "$CMDMAN_CMD_DATA_DIR/config.json"
`,
	)
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	exitCode, _ := info["ExitCode"].(float64)
	if exitCode != 0 {
		t.Fatalf("expected injected cmdman context environment, exit_code=%v", exitCode)
	}

	cfg, _ := info["Config"].(map[string]any)
	envList, _ := cfg["env"].([]any)
	want := map[string]string{
		cmdman.ENV_CMDMAN_DATA_DIR:     env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR:  env.runtimeDir,
		cmdman.ENV_CMDMAN_CMD_DATA_DIR: filepath.Join(env.dataHome, "commands", id),
		cmdman.ENV_CMDMAN_CMD_ID:       id,
	}
	counts := map[string]int{}
	for _, e := range envList {
		s, _ := e.(string)
		for key, value := range want {
			prefix := key + "="
			if strings.HasPrefix(s, prefix) {
				counts[key]++
				if s != prefix+value {
					t.Errorf("expected %s, got %s", prefix+value, s)
				}
			}
		}
	}
	for key := range want {
		if counts[key] != 1 {
			t.Fatalf("expected exactly one %s entry, got %d in %v", key, counts[key], envList)
		}
	}
}

func TestRun_AutoRemove(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--rm", "--", "/bin/sh", "-c", "echo ephemeral")

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

	env.run(ctx, "run", "-n", "unique-name", "--", "/bin/sh", "-c", "sleep 60")
	t.Cleanup(func() { env.cleanupCommand(ctx, "unique-name") })

	env.waitForState(ctx, "unique-name", "running", defaultTimeout)

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
	cfg, _ := info["Config"].(map[string]any)
	scrollback, _ := cfg["scrollback_bytes"].(float64)
	if scrollback != 2048 {
		t.Errorf("expected scrollback_bytes=2048, got %v", scrollback)
	}
}

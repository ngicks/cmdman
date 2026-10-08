package cmdman_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// composeStopConfigYAML returns a one-command compose file whose command exits 0
// and carries the given stop_grace_period and a stop command.
func composeStopConfigYAML(name, gracePeriod string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sh, -c, "echo alpha"]
    stop_grace_period: %s
    stop: [true]
`, name, gracePeriod)
}

// composeStopConfig returns the stored stop_timeout and stop_command args of
// the compose command alpha.
func composeStopConfig(
	ctx context.Context,
	t *testing.T,
	env *testEnv,
	id string,
) (timeout any, args any) {
	t.Helper()
	cfg, _ := env.inspectJSON(ctx, id)["Config"].(map[string]any)
	stop, _ := cfg["stop_command"].(map[string]any)
	return cfg["stop_timeout"], stop["args"]
}

func TestComposeStopConfig_StoredOnUp(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-stop-config"
	composePath := writeComposeFile(t, wd, composeStopConfigYAML(project, "2s"))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	if _, stderr, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"up",
	); err != nil {
		t.Fatalf("compose up failed: %v\nstderr:\n%s", err, stderr)
	}

	id := composeCommandID(ctx, env, wd, project, "alpha")
	if id == "" {
		t.Fatalf("alpha was not created by up")
	}
	timeout, args := composeStopConfig(ctx, t, env, id)
	if timeout != "2s" {
		t.Errorf("stop_timeout = %#v, want %q", timeout, "2s")
	}
	if want := []any{"true"}; !reflect.DeepEqual(args, want) {
		t.Errorf("stop_command args = %#v, want %#v", args, want)
	}
}

func TestComposeStopConfig_RecreateOnGracePeriodChange(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-stop-config-recreate"
	composePath := writeComposeFile(t, wd, composeStopConfigYAML(project, "2s"))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	if _, stderr, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"up",
	); err != nil {
		t.Fatalf("compose up #1 failed: %v\nstderr:\n%s", err, stderr)
	}
	idBefore := composeCommandID(ctx, env, wd, project, "alpha")
	if idBefore == "" {
		t.Fatalf("alpha was not created by up #1")
	}
	// Wait for the short-running command to exit so recreate isn't skipped.
	env.waitForState(ctx, idBefore, "exited", 5*time.Second)

	writeComposeFile(t, wd, composeStopConfigYAML(project, "3s"))

	stdout, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up")
	if err != nil {
		t.Fatalf("compose up #2 failed: %v\nstdout:\n%s", err, stdout)
	}
	if !progressReached(parseProgress(t, stdout), "alpha", "recreated") {
		t.Fatalf("expected recreate for alpha after a stop_grace_period edit; got:\n%s", stdout)
	}

	idAfter := composeCommandID(ctx, env, wd, project, "alpha")
	if idAfter == "" || idAfter == idBefore {
		t.Fatalf("alpha id should have changed after recreate: before %s, after %s",
			idBefore, idAfter)
	}
	if timeout, _ := composeStopConfig(ctx, t, env, idAfter); timeout != "3s" {
		t.Fatalf("recreated alpha stop_timeout = %#v, want %q", timeout, "3s")
	}
}

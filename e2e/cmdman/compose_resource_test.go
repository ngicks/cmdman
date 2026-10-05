package cmdman_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// composeResourceYAML declares one long-running command web with scale
// replicas.
func composeResourceYAML(name string, scale int) string {
	return fmt.Sprintf(`name: %s
commands:
  web:
    scale: %d
    args: [sleep, "300"]
`, name, scale)
}

// resourceHolders returns the `ls` JSON entries of the resource holders of a
// project.
func resourceHolders(ctx context.Context, e *testEnv, wd, project string) []map[string]any {
	return e.lsJSON(ctx,
		"-l", "cmdman.compose.intermediate=holder",
		"-l", "cmdman.compose.hooks.workdir="+wd,
		"-l", "cmdman.compose.hooks.project="+project,
	)
}

// cleanupResourceHolders removes the resource holders of a project, which the
// project labels cleanupProject goes by do not select.
func cleanupResourceHolders(ctx context.Context, e *testEnv, wd, project string) {
	for _, h := range resourceHolders(ctx, e, wd, project) {
		e.exec(ctx, "rm", "-f", h["ID"].(string))
	}
}

func TestComposeResourceSingleReplica(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-resource-single"
	composePath := writeComposeFile(t, wd, composeResourceYAML(project, 1))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupResourceHolders(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	replicaID := composeReplica(ctx, env, wd, project, "web", 1)["ID"].(string)
	env.waitForState(ctx, replicaID, "running", defaultTimeout)

	compose("resource", "get", "web", "scratch").ExpectFail(ctx, t, "no such resource")

	compose("resource", "set", "web", "scratch", "/tmp/a").Run(ctx, t)
	if got := compose("resource", "get", "web", "scratch").Run(ctx, t); got != "/tmp/a" {
		t.Fatalf("get after set = %q, want /tmp/a", got)
	}
	compose("resource", "set", "web", "scratch", "/tmp/b").Run(ctx, t)
	if got := compose("resource", "get", "web", "scratch").Run(ctx, t); got != "/tmp/b" {
		t.Fatalf("get after replacing set = %q, want /tmp/b", got)
	}

	holders := resourceHolders(ctx, env, wd, project)
	if len(holders) != 1 {
		t.Fatalf("want one holder, got %d: %v", len(holders), holders)
	}
	holderName := holders[0]["Name"].(string)
	holderID := holders[0]["ID"].(string)

	t.Run(
		"holder is listed by ls and compose ps but left out of compose verbs",
		func(t *testing.T) {
			ls := env.Cmd("ls", "--format", "{{.Name}} {{.State}}").Run(ctx, t)
			if !strings.Contains(ls, holderName+" created") {
				t.Fatalf("ls should list the holder %s as created:\n%s", holderName, ls)
			}
			want := replicaID + ":\n" + holderID + ":holder"
			if ps := compose(
				"ps",
				"--format",
				"{{.ID}}:{{.Intermediate}}",
			).Run(ctx, t); ps != want {
				t.Fatalf("compose ps should list the replica, then the holder, got:\n%s", ps)
			}
			compose("stop").Run(ctx, t)
			if st := composeReplicaState(ctx, env, wd, project, "web", 1); !isStopped(st) {
				t.Fatalf("compose stop should stop the replica, got %q", st)
			}
			if st := env.inspectJSON(ctx, holderID)["State"]; st != "created" {
				t.Fatalf("compose stop should leave the holder alone, got state %v", st)
			}
		},
	)

	t.Run("holder outlives its replica", func(t *testing.T) {
		env.Cmd("rm", "-f", replicaID).Run(ctx, t)
		if got := compose("resource", "get", "web", "scratch").Run(ctx, t); got != "/tmp/b" {
			t.Fatalf("get after the replica is gone = %q, want /tmp/b", got)
		}
	})

	t.Run("unset", func(t *testing.T) {
		compose("resource", "unset", "web", "scratch").Run(ctx, t)
		compose("resource", "get", "web", "scratch").ExpectFail(ctx, t, "no such resource")
		compose("resource", "unset", "web", "scratch").Run(ctx, t)
		if left := resourceHolders(ctx, env, wd, project); len(left) != 0 {
			t.Fatalf("unset should remove the holder, %d left", len(left))
		}
	})
}

func TestComposeResourceScaledReplicas(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-resource-scaled"
	composePath := writeComposeFile(t, wd, composeResourceYAML(project, 2))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupResourceHolders(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("create").Run(ctx, t)

	compose("resource", "set", "web", "scratch", "two").ExpectFail(ctx, t, "--scale 1..2")
	compose("resource", "get", "web", "scratch").ExpectFail(ctx, t, "--scale 1..2")
	compose("resource", "set", "--scale", "0", "web", "scratch", "x").
		ExpectFail(ctx, t, "--scale must be 1 or greater")

	compose("resource", "set", "--scale", "2", "web", "scratch", "two").Run(ctx, t)
	if got := compose(
		"resource",
		"get",
		"--scale",
		"2",
		"web",
		"scratch",
	).Run(ctx, t); got != "two" {
		t.Fatalf("get --scale 2 = %q, want two", got)
	}
	compose("resource", "get", "--scale", "1", "web", "scratch").
		ExpectFail(ctx, t, "no such resource")

	// A replica or hook of web replica 1 runs with these, wherever its working
	// directory is.
	replica1Env := []string{
		"CMDMAN_COMPOSE_WORK_DIR=" + wd,
		"CMDMAN_COMPOSE_PROJECT=" + project,
		"CMDMAN_COMPOSE_COMMAND=web",
		"CMDMAN_COMPOSE_SCALE_INDEX=1",
	}
	elsewhere := t.TempDir()
	resource := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "resource"}, args...)...).
			InDir(elsewhere).WithEnv(replica1Env...)
	}

	resource("set", "web", "scratch", "one").Run(ctx, t)
	if got := resource("get", "web", "scratch").Run(ctx, t); got != "one" {
		t.Fatalf("own-replica get = %q, want one", got)
	}
	if got := compose(
		"resource",
		"get",
		"--scale",
		"1",
		"web",
		"scratch",
	).Run(ctx, t); got != "one" {
		t.Fatalf("the own-replica set should land on replica 1, got %q", got)
	}
	if got := resource("get", "--scale", "2", "web", "scratch").Run(ctx, t); got != "two" {
		t.Fatalf("an explicit --scale wins over the environment, got %q", got)
	}
	if n := len(resourceHolders(ctx, env, wd, project)); n != 2 {
		t.Fatalf("want a holder per replica, got %d", n)
	}
}

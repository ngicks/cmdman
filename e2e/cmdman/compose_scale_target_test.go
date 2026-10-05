package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// composeScaleTargetYAML defines one TTY command with two replicas. Each run of
// a replica appends a line to <wd>/runs-<index>.txt, prints its index, then
// blocks on a line of input before idling, so a test can tell which replica a
// verb reached from run counts, log lines and input.
func composeScaleTargetYAML(name, wd string) string {
	runs := shellQuote(filepath.Join(wd, "runs-"))
	return fmt.Sprintf(`name: %s
commands:
  web:
    tty: true
    scale: 2
    args:
      - sh
      - -c
      - >-
        echo run >> %s$$CMDMAN_COMPOSE_SCALE_INDEX.txt;
        echo replica-$$CMDMAN_COMPOSE_SCALE_INDEX;
        read _;
        echo rx-$$CMDMAN_COMPOSE_SCALE_INDEX;
        sleep 300
`, name, runs)
}

// composeScaleDependencyYAML defines two scaled commands where every replica of
// app waits for setup to complete successfully. Each run of a replica appends a
// line to <wd>/<command>-<index>.txt.
func composeScaleDependencyYAML(name, wd string) string {
	setup := shellQuote(filepath.Join(wd, "setup-"))
	app := shellQuote(filepath.Join(wd, "app-"))
	return fmt.Sprintf(`name: %s
commands:
  setup:
    scale: 2
    args: [sh, -c, "echo run >> %s$$CMDMAN_COMPOSE_SCALE_INDEX.txt; sleep 0.5"]
  app:
    scale: 2
    args: [sh, -c, "echo run >> %s$$CMDMAN_COMPOSE_SCALE_INDEX.txt; sleep 300"]
    after:
      setup:
        condition: completed_successfully
`, name, setup, app)
}

// composeReplica returns the `ls` JSON entry of replica idx of a compose
// command, or nil when that replica does not exist.
func composeReplica(
	ctx context.Context,
	e *testEnv,
	wd, project, command string,
	idx int,
) map[string]any {
	entries := e.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
		"-l", "cmdman.compose.command="+command,
		"-l", "cmdman.compose.scale-index="+strconv.Itoa(idx),
	)
	if len(entries) == 0 {
		return nil
	}
	return entries[0]
}

func composeReplicaState(
	ctx context.Context,
	e *testEnv,
	wd, project, command string,
	idx int,
) string {
	entry := composeReplica(ctx, e, wd, project, command, idx)
	if entry == nil {
		return ""
	}
	state, _ := entry["State"].(string)
	return state
}

// runCount returns how many times a counter file was appended to.
func runCount(t *testing.T, path string) int {
	t.Helper()
	return countNonEmptyLines(readFile(t, path))
}

func isStopped(state string) bool {
	return state == "exited" || state == "failed"
}

// TestComposeScaleFlagTargetsOneReplica drives every verb that takes --scale
// against a two-replica command and checks that each one reaches replica 2
// only.
func TestComposeScaleFlagTargetsOneReplica(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-scale-flag"
	composePath := writeComposeFile(t, wd, composeScaleTargetYAML(project, wd))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	runs1 := filepath.Join(wd, "runs-1.txt")
	runs2 := filepath.Join(wd, "runs-2.txt")

	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	id1 := composeReplica(ctx, env, wd, project, "web", 1)["ID"].(string)
	id2 := composeReplica(ctx, env, wd, project, "web", 2)["ID"].(string)
	env.waitForState(ctx, id1, "running", defaultTimeout)
	env.waitForState(ctx, id2, "running", defaultTimeout)
	waitUntil(t, defaultTimeout, func() bool {
		return runCount(t, runs1) == 1 && runCount(t, runs2) == 1
	}, "both replicas should have run once")

	// replica1Untouched checks that replica 1 still runs its first run.
	replica1Untouched := func(t *testing.T) {
		t.Helper()
		if st := composeReplicaState(ctx, env, wd, project, "web", 1); st != "running" {
			t.Fatalf("replica 1 should still be running, got %q", st)
		}
		if n := runCount(t, runs1); n != 1 {
			t.Fatalf("replica 1 should have run once, ran %d times", n)
		}
	}

	t.Run("ps", func(t *testing.T) {
		out := compose("ps", "--scale", "2", "web", "--format", "{{.ID}}").Run(ctx, t)
		if out != id2 {
			t.Fatalf("ps --scale 2 should list replica 2 (%s) only, got:\n%s", id2, out)
		}
	})

	t.Run("status", func(t *testing.T) {
		out := compose("status", "--scale", "2", "web", "--format", "{{.ID}}").Run(ctx, t)
		if out != id2 {
			t.Fatalf("status --scale 2 should list replica 2 (%s) only, got:\n%s", id2, out)
		}
	})

	t.Run("inspect", func(t *testing.T) {
		arr := parseJSONArray(t, compose("inspect", "--scale", "2", "web").Run(ctx, t))
		if len(arr) != 1 || arr[0]["ID"] != id2 {
			t.Fatalf("inspect --scale 2 should return replica 2 (%s) only, got %#v", id2, arr)
		}
	})

	t.Run("logs", func(t *testing.T) {
		waitUntil(t, defaultTimeout, func() bool {
			return strings.Contains(compose("logs", "--scale", "2", "web").Run(ctx, t), "replica-2")
		}, "replica 2 output did not appear")
		out := compose("logs", "--scale", "2", "web").Run(ctx, t)
		if strings.Contains(out, "replica-1") {
			t.Fatalf("logs --scale 2 leaked replica 1 output:\n%s", out)
		}
	})

	t.Run("send-keys", func(t *testing.T) {
		out := compose("send-keys", "--scale", "2", "web", "--", "Enter").Run(ctx, t)
		if strings.Count(out, "sent") != 1 || !hasResultLine(out, "sent", "web-2") {
			t.Fatalf("send-keys --scale 2 should report web-2 only, got:\n%s", out)
		}
		waitUntil(t, defaultTimeout, func() bool {
			return strings.Contains(compose("logs", "--scale", "2", "web").Run(ctx, t), "rx-2")
		}, "replica 2 did not receive the key")
		if out := compose(
			"logs",
			"--scale",
			"1",
			"web",
		).Run(ctx, t); strings.Contains(
			out,
			"rx-1",
		) {
			t.Fatalf("replica 1 received the key sent to replica 2:\n%s", out)
		}
	})

	t.Run("events", func(t *testing.T) {
		out := compose("events", "--scale", "2", "web", "--no-follow").Run(ctx, t)
		if !strings.Contains(out, id2) {
			t.Fatalf("events --scale 2 should report replica 2 (%s):\n%s", id2, out)
		}
		if strings.Contains(out, id1) {
			t.Fatalf("events --scale 2 leaked replica 1 (%s):\n%s", id1, out)
		}
	})

	t.Run("restart", func(t *testing.T) {
		out := compose("restart", "--scale", "2", "web").Run(ctx, t)
		if strings.Count(out, "restarted") != 1 || !hasResultLine(out, "restarted", "web-2") {
			t.Fatalf("restart --scale 2 should report web-2 only, got:\n%s", out)
		}
		waitUntil(t, defaultTimeout, func() bool {
			return runCount(t, runs2) == 2
		}, "replica 2 should have run again")
		env.waitForState(ctx, id2, "running", defaultTimeout)
		replica1Untouched(t)
	})

	t.Run("stop", func(t *testing.T) {
		compose("stop", "--scale", "2", "web").Run(ctx, t)
		if st := composeReplicaState(ctx, env, wd, project, "web", 2); !isStopped(st) {
			t.Fatalf("replica 2 should be stopped, got %q", st)
		}
		replica1Untouched(t)
	})

	t.Run("wait", func(t *testing.T) {
		// Replica 1 keeps running, so a wait that reached it would not return.
		out := compose("wait", "--scale", "2", "web").WithTimeout(defaultTimeout).Run(ctx, t)
		if strings.Count(out, "done") != 1 || !strings.HasPrefix(
			strings.Join(strings.Fields(out), " "), "done web-2") {
			t.Fatalf("wait --scale 2 should report web-2 only, got:\n%s", out)
		}
	})

	t.Run("start", func(t *testing.T) {
		compose("start", "--scale", "2", "web").Run(ctx, t)
		env.waitForState(ctx, id2, "running", defaultTimeout)
		waitUntil(t, defaultTimeout, func() bool {
			return runCount(t, runs2) == 3
		}, "replica 2 should have run a third time")
		replica1Untouched(t)
	})

	t.Run("signal", func(t *testing.T) {
		out := compose("signal", "--scale", "2", "web", "--signal", "SIGTERM").Run(ctx, t)
		if strings.Count(out, "signaled") != 1 || !hasResultLine(out, "signaled", "web-2") {
			t.Fatalf("signal --scale 2 should report web-2 only, got:\n%s", out)
		}
		waitUntil(t, defaultTimeout, func() bool {
			return isStopped(composeReplicaState(ctx, env, wd, project, "web", 2))
		}, "replica 2 should stop on SIGTERM")
		replica1Untouched(t)
	})

	t.Run("create recreates only the removed replica", func(t *testing.T) {
		name2 := composeReplica(ctx, env, wd, project, "web", 2)["Name"].(string)
		env.Cmd("rm", "-f", name2).Run(ctx, t)

		out := compose("create", "--scale", "2", "web").Run(ctx, t)
		if !strings.Contains(out, "web-2") || strings.Contains(out, "web-1") {
			t.Fatalf("create --scale 2 should act on web-2 only, got:\n%s", out)
		}
		if st := composeReplicaState(ctx, env, wd, project, "web", 2); st != "created" {
			t.Fatalf("replica 2 should be created again, got %q", st)
		}
		replica1Untouched(t)
	})

	t.Run("up recreates and starts only the removed replica", func(t *testing.T) {
		name2 := composeReplica(ctx, env, wd, project, "web", 2)["Name"].(string)
		env.Cmd("rm", "-f", name2).Run(ctx, t)

		out := compose("up", "--scale", "2", "web").Run(ctx, t)
		events := parseProgress(t, out)
		if !progressReached(events, "web-2", "created") ||
			!progressReached(events, "web-2", "running") {
			t.Fatalf("up --scale 2 should create and start web-2, got:\n%s", out)
		}
		for _, ev := range events {
			if ev.Command != "web-2" {
				t.Fatalf("up --scale 2 reported %q, want web-2 only:\n%s", ev.Command, out)
			}
		}
		replica2 := composeReplica(ctx, env, wd, project, "web", 2)
		env.waitForState(ctx, replica2["ID"].(string), "running", defaultTimeout)
		if got := composeReplica(ctx, env, wd, project, "web", 1)["ID"]; got != id1 {
			t.Fatalf("replica 1 should keep its ID %s, got %v", id1, got)
		}
		replica1Untouched(t)
	})
}

// TestComposeScaleFlagMisuse checks the usage errors of --scale and that down
// does not take it.
func TestComposeScaleFlagMisuse(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-scale-misuse"
	composePath := writeComposeFile(t, wd, composeScaleTargetYAML(project, wd))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}
	compose("create").Run(ctx, t)

	compose("stop", "--scale", "2", "web", "other").
		ExpectFail(ctx, t, "--scale needs exactly one COMMAND", "web other")
	compose("ps", "--scale", "2").
		ExpectFail(ctx, t, "--scale needs exactly one COMMAND, got none")
	compose("send-keys", "--scale", "2", "web", "other", "--", "Enter").
		ExpectFail(ctx, t, "--scale needs exactly one COMMAND")
	compose("logs", "--scale", "0", "web").
		ExpectFail(ctx, t, "--scale must be 1 or greater")

	// A stored replica bounds the range of the verbs that act on stored
	// commands; the declared scale bounds create and up.
	compose("inspect", "--scale", "3", "web").ExpectFail(ctx, t, `"web"`, "1..2")
	compose("up", "--scale", "3", "web").ExpectFail(ctx, t, `"web"`, "1..2")

	compose("down", "--scale", "1", "web").ExpectFail(ctx, t, "unknown flag: --scale")
	if help := compose("down", "--help").Run(ctx, t); !strings.Contains(help, "compose scale") {
		t.Fatalf("down --help should point at compose scale, got:\n%s", help)
	}
}

// TestComposeStartScaleWithCompletedDependency targets one replica of a command
// that a dependent waits on with completed_successfully, then one replica of
// that dependent.
func TestComposeStartScaleWithCompletedDependency(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-scale-dep"
	composePath := writeComposeFile(t, wd, composeScaleDependencyYAML(project, wd))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	counter := func(command string, idx int) string {
		return filepath.Join(wd, fmt.Sprintf("%s-%d.txt", command, idx))
	}

	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}
	compose("create").Run(ctx, t)

	// The dependent is outside a start's closure, so starting one replica of
	// setup runs that replica alone and leaves app as it was.
	out := compose("start", "--scale", "2", "setup").Run(ctx, t)
	for _, ev := range parseProgress(t, out) {
		if ev.Command != "setup-2" {
			t.Fatalf("start --scale 2 setup reported %q, want setup-2 only:\n%s", ev.Command, out)
		}
	}
	setup2 := composeReplica(ctx, env, wd, project, "setup", 2)["ID"].(string)
	env.waitForState(ctx, setup2, "exited", defaultTimeout)
	if n := runCount(t, counter("setup", 2)); n != 1 {
		t.Fatalf("setup replica 2 should have run once, ran %d times", n)
	}
	if st := composeReplicaState(ctx, env, wd, project, "setup", 1); st != "created" {
		t.Fatalf("setup replica 1 should stay created, got %q", st)
	}
	for idx := 1; idx <= 2; idx++ {
		if st := composeReplicaState(ctx, env, wd, project, "app", idx); st != "created" {
			t.Fatalf("app replica %d should stay created, got %q", idx, st)
		}
	}

	// Starting one replica of app pulls every replica of setup in and waits for
	// all of them to complete before that one app replica starts.
	compose("start", "--scale", "2", "app").Run(ctx, t)
	app2 := composeReplica(ctx, env, wd, project, "app", 2)["ID"].(string)
	env.waitForState(ctx, app2, "running", defaultTimeout)
	if n := runCount(t, counter("setup", 1)); n != 1 {
		t.Fatalf("setup replica 1 should have run once, ran %d times", n)
	}
	if n := runCount(t, counter("setup", 2)); n != 2 {
		t.Fatalf("setup replica 2 should have run twice, ran %d times", n)
	}
	if st := composeReplicaState(ctx, env, wd, project, "app", 1); st != "created" {
		t.Fatalf("app replica 1 should stay created, got %q", st)
	}
	if n := runCount(t, counter("app", 1)); n != 0 {
		t.Fatalf("app replica 1 should not have run, ran %d times", n)
	}
}

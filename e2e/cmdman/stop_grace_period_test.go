package cmdman_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A stop of a command that ignores SIGTERM lasts its whole grace period. The
// bounds keep a 2 s grace period apart from an immediate stop and from the 10 s
// default, with room for a loaded machine.
const (
	graceMin = 1500 * time.Millisecond
	graceMax = 6 * time.Second
)

// termIgnoringScript ignores SIGTERM and then records its pid in pidFile, so a
// test that has read the pid knows the stop signal changes nothing. exec keeps
// the pid and carries the ignored disposition into sleep. dollars is how $
// reaches the shell: "$" on the command line, "$$" through compose
// interpolation.
func termIgnoringScript(pidFile, dollars string) string {
	return fmt.Sprintf(`trap "" TERM; echo %s%s > '%s'; exec sleep 300`, dollars, dollars, pidFile)
}

// timedExec runs c and returns its result and how long it took.
func timedExec(ctx context.Context, c *Cmd) (Result, time.Duration) {
	start := time.Now()
	res := c.Exec(ctx)
	return res, time.Since(start)
}

func assertDuration(t *testing.T, what string, got, lo, hi time.Duration) {
	t.Helper()
	if got < lo || got >= hi {
		t.Errorf("%s took %s, want at least %s and less than %s", what, got, lo, hi)
	}
}

// runTermIgnoring runs a command that ignores SIGTERM under name with the given
// --stop-timeout, waits for it to run, and returns its pid file.
func runTermIgnoring(
	ctx context.Context,
	t *testing.T,
	env *testEnv,
	name, stopTimeout string,
) string {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	env.Cmd("run", "-n", name, "--stop-timeout", stopTimeout, "--",
		"/bin/sh", "-c", termIgnoringScript(pidFile, "$")).Run(ctx, t)
	t.Cleanup(func() { env.cleanupCommand(context.Background(), name) })
	env.waitForState(ctx, name, "running", defaultTimeout)
	pid := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(pid) })
	return pidFile
}

func TestStop_WaitsStoredStopTimeout(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	runTermIgnoring(ctx, t, env, "grace-stop", "2s")

	res, took := timedExec(ctx, env.Cmd("stop", "grace-stop"))
	if res.Err != nil {
		t.Fatalf("stop failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	assertDuration(t, "stop", took, graceMin, graceMax)
	if state := env.inspectJSON(ctx, "grace-stop")["State"]; state == "running" {
		t.Errorf("command still running after stop")
	}
}

func TestRestart_WaitsStoredStopTimeout(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	pidFile := runTermIgnoring(ctx, t, env, "grace-restart", "2s")
	must(t, os.Remove(pidFile))

	res, took := timedExec(ctx, env.Cmd("restart", "grace-restart"))
	if res.Err != nil {
		t.Fatalf("restart failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	assertDuration(t, "restart", took, graceMin, graceMax)
	pid := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(pid) })
	env.waitForState(ctx, "grace-restart", "running", defaultTimeout)
}

func TestStop_TimeoutFlagOverridesStored(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	runTermIgnoring(ctx, t, env, "grace-override", "5s")

	res, took := timedExec(ctx, env.Cmd("stop", "-t", "1", "grace-override"))
	if res.Err != nil {
		t.Fatalf("stop -t 1 failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	// Below the stored 5 s with room for a loaded machine.
	assertDuration(t, "stop -t 1", took, 700*time.Millisecond, 4*time.Second)
}

func TestStop_TimeoutFlagNonPositiveRejected(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	env.Run(ctx, "grace-reject", "/bin/sh", "-c", "sleep 300")
	env.waitForState(ctx, "grace-reject", "running", defaultTimeout)
	monitor := monitorPID(t, env.inspectJSON(ctx, "grace-reject"))

	for _, args := range [][]string{
		{"stop", "-t", "0"},
		{"stop", "-t", "-3"},
		{"restart", "-t", "0"},
		{"restart", "-t", "-3"},
	} {
		env.Cmd(slices.Concat(args, []string{"grace-reject"})...).
			ExpectFail(ctx, t, "--timeout", "timeout must be positive")
	}

	// sleep ends at once on a stop signal, and a restart replaces the monitor.
	time.Sleep(500 * time.Millisecond)
	info := env.inspectJSON(ctx, "grace-reject")
	if info["State"] != "running" {
		t.Fatalf("a rejected stop or restart touched the command: state %v", info["State"])
	}
	if got := monitorPID(t, info); got != monitor {
		t.Fatalf("a rejected restart replaced the monitor: pid %d, was %d", got, monitor)
	}
}

// composeGraceYAML returns a one-command project whose command runs script with
// a 2 s stop_grace_period.
func composeGraceYAML(project, script string) string {
	return fmt.Sprintf(`name: %s
commands:
  holder:
    args: [sh, -c, %q]
    stop_grace_period: 2s
`, project, script)
}

// upComposeGrace brings up a project of composeGraceYAML and returns the
// arguments that select the project and the ID of its command once it runs.
func upComposeGrace(
	ctx context.Context,
	t *testing.T,
	env *testEnv,
	project, script string,
) (selection []string, id string) {
	t.Helper()
	wd := composeWorkdir(t)
	composePath := writeComposeFile(t, wd, composeGraceYAML(project, script))
	t.Cleanup(func() { cleanupProject(context.Background(), env, wd, project) })
	selection = []string{"compose", "--workdir", wd, "-f", composePath}
	env.Cmd(append(selection, "up")...).Run(ctx, t)
	id = composeCommandID(ctx, env, wd, project, "holder")
	if id == "" {
		t.Fatalf("holder was not created by up")
	}
	env.waitForState(ctx, id, "running", defaultTimeout)
	return selection, id
}

func TestCompose_WaitsStopGracePeriod(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"stop", "down", "restart"} {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			ctx := testContext(t)
			env := newTestEnv(t)
			pidFile := filepath.Join(t.TempDir(), "pid")
			selection, id := upComposeGrace(
				ctx, t, env, "tc-grace-"+op, termIgnoringScript(pidFile, "$$"))
			pid := readPidFile(t, pidFile)
			t.Cleanup(func() { killIfAlive(pid) })
			must(t, os.Remove(pidFile))

			res, took := timedExec(ctx, env.Cmd(slices.Concat(selection, []string{op})...))
			if res.Err != nil {
				t.Fatalf("compose %s failed: %v\nstdout:\n%s\nstderr:\n%s",
					op, res.Err, res.Stdout, res.Stderr)
			}
			assertDuration(t, "compose "+op, took, graceMin, graceMax)

			switch op {
			case "stop":
				if state := env.inspectJSON(ctx, id)["State"]; state == "running" {
					t.Errorf("holder still running after compose stop")
				}
			case "down":
				if res := env.Cmd("inspect", id).Exec(ctx); res.Err == nil {
					t.Errorf("holder still stored after compose down")
				}
			case "restart":
				pid := readPidFile(t, pidFile)
				t.Cleanup(func() { killIfAlive(pid) })
				env.waitForState(ctx, id, "running", defaultTimeout)
			}
		})
	}
}

func TestCompose_TimeoutFlagNonPositiveRejected(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	selection, id := upComposeGrace(ctx, t, env, "tc-grace-reject", "sleep 300")
	monitor := monitorPID(t, env.inspectJSON(ctx, id))

	for _, args := range [][]string{
		{"stop", "-t", "0"},
		{"down", "-t", "-3"},
		{"restart", "-t", "0"},
	} {
		env.Cmd(slices.Concat(selection, args)...).
			ExpectFail(ctx, t, "--timeout", "timeout must be positive")
	}

	// sleep ends at once on a stop signal, and a restart replaces the monitor.
	time.Sleep(500 * time.Millisecond)
	info := env.inspectJSON(ctx, id)
	if info["State"] != "running" {
		t.Fatalf("a rejected compose verb touched holder: state %v", info["State"])
	}
	if got := monitorPID(t, info); got != monitor {
		t.Fatalf("a rejected compose restart replaced the monitor: pid %d, was %d", got, monitor)
	}
}

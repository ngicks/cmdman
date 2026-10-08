package cmdman_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// forceKilledStderr is what `cmdman stop` prints for a command it force-killed.
const forceKilledStderr = "force-killed after the grace period; detached processes may survive"

// forceKilledWarning is the warning `cmdman inspect` lists for a run a stop
// force-killed.
const forceKilledWarning = "the stop signal did not end the command within its grace period; " +
	"resorted to SIGKILL; detached processes may survive"

// stateDetail returns the persisted state of the command name as inspect shows
// it.
func stateDetail(ctx context.Context, t *testing.T, env *testEnv, name string) map[string]any {
	t.Helper()
	detail, _ := env.inspectJSON(ctx, name)["StateJSON"].(map[string]any)
	if detail == nil {
		t.Fatalf("inspect %s shows no state detail", name)
	}
	return detail
}

// stateWarnings returns the warnings the persisted state detail lists.
func stateWarnings(detail map[string]any) []string {
	raw, _ := detail["warnings"].([]any)
	out := make([]string, 0, len(raw))
	for _, w := range raw {
		s, _ := w.(string)
		out = append(out, s)
	}
	return out
}

// A stop whose grace period runs out on a command that ignores the stop
// signal says so everywhere: the stop's own stderr, one stopped event for the
// SIGKILL with the reason, the exited event, and the state inspect shows.
func TestStop_ForceKillRecorded(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	const name = "force-killed"
	runTermIgnoring(ctx, t, env, name, "1s")
	id := env.resolvedID(ctx, name)

	res := env.Cmd("stop", name).Exec(ctx)
	if res.Err != nil {
		t.Fatalf("stop failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	if want := "stop " + id + ": " + forceKilledStderr; !strings.Contains(res.Stderr, want) {
		t.Errorf("stop stderr = %q, want a line %q", res.Stderr, want)
	}

	stops := stoppedEvents(t, ctx, env, name)
	if forced := forcedKillStops(stops); len(forced) != 1 {
		t.Errorf("want exactly one forced-kill stopped event, got %d:\n%v", len(forced), stops)
	}
	if len(stops) != 2 {
		t.Errorf("want the client's stop and the forced kill, got %d stopped events:\n%v",
			len(stops), stops)
	}
	if exited := lastEvent(t, ctx, env, name, "exited"); exited.Attrs["force_killed"] != "true" {
		t.Errorf("exited event does not say force_killed: %s", exited.raw)
	}

	detail := stateDetail(ctx, t, env, name)
	if detail["force_killed"] != true {
		t.Errorf("inspect force_killed = %#v, want true", detail["force_killed"])
	}
	if warnings := stateWarnings(detail); !slices.Contains(warnings, forceKilledWarning) {
		t.Errorf("inspect warnings = %q, want %q among them", warnings, forceKilledWarning)
	}
}

// A SIGKILL asked for in its own right is no stop that ran out its grace
// period, and nothing records it as one.
func TestStop_ExplicitKillIsNoForceKill(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	const name = "killed-explicitly"
	runTermIgnoring(ctx, t, env, name, "1s")

	res := env.Cmd("stop", "-s", "KILL", name).Exec(ctx)
	if res.Err != nil {
		t.Fatalf("stop -s KILL failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	if strings.Contains(res.Stderr, "force-killed") {
		t.Errorf("stop -s KILL reported a forced kill: %q", res.Stderr)
	}
	env.waitForState(ctx, name, "exited", defaultTimeout)

	if forced := forcedKillStops(stoppedEvents(t, ctx, env, name)); len(forced) != 0 {
		t.Errorf("stop -s KILL recorded forced-kill stopped events: %v", forced)
	}
	if exited := lastEvent(t, ctx, env, name, "exited"); exited.Attrs["force_killed"] != "" {
		t.Errorf("exited event says force_killed: %s", exited.raw)
	}
	detail := stateDetail(ctx, t, env, name)
	if v, ok := detail["force_killed"]; ok {
		t.Errorf("inspect force_killed = %#v, want it absent", v)
	}
	if warnings := stateWarnings(detail); len(warnings) != 0 {
		t.Errorf("inspect warnings = %q, want none", warnings)
	}
}

// compose down reports the replica it force-killed on the replica's stopped
// progress record, before it removes the replica and its state.
func TestComposeDown_ForceKillProgress(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	selection, _ := upComposeGrace(
		ctx, t, env, "tc-force-kill-down", termIgnoringScript(pidFile, "$$"))
	pid := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(pid) })

	res := env.Cmd(slices.Concat(selection, []string{"down", "--progress", "json", "-t", "1"})...).
		Exec(ctx)
	if res.Err != nil {
		t.Fatalf(
			"compose down failed: %v\nstdout:\n%s\nstderr:\n%s",
			res.Err,
			res.Stdout,
			res.Stderr,
		)
	}

	var stopped []progressEvent
	for _, ev := range parseProgress(t, res.Stdout) {
		if ev.Command == "holder" && ev.Phase == "stopped" {
			stopped = append(stopped, ev)
		}
	}
	if len(stopped) != 1 || !stopped[0].ForceKilled {
		t.Fatalf("want one stopped record for holder with forceKilled, got %+v\nstdout:\n%s",
			stopped, res.Stdout)
	}
	if !strings.Contains(res.Stdout, `"forceKilled":true`) {
		t.Errorf("the progress record does not carry forceKilled:\n%s", res.Stdout)
	}
}

package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// progressError returns the error text a command's error event carried, empty
// when the trace has no error event for it.
func progressError(events []progressEvent, command string) string {
	for _, ev := range events {
		if ev.Command == command && ev.Phase == "error" {
			return ev.Error
		}
	}
	return ""
}

// monitorPID reads the supervising process id out of an inspect result.
func monitorPID(t *testing.T, info map[string]any) int {
	t.Helper()
	state, _ := info["StateJSON"].(map[string]any)
	pid, ok := state["monitor_pid"].(float64)
	if !ok || pid <= 0 {
		t.Fatalf("no monitor pid in inspect output: %v", info["StateJSON"])
	}
	return int(pid)
}

// TestComposeStop_ReportsTargetFailure pins a compose stop against a target it
// cannot stop. Service.Stop reports such a target in its result slice rather
// than in its returned error, so a compose stop that only read the error would
// print the command as stopped and exit 0.
//
// The failure is built from the two halves the stop escalation needs: a child
// that ignores SIGTERM, so the first signal changes nothing and the wait runs
// out, and a monitor that is gone by the time the SIGKILL escalation looks for
// it, so the second signal has no way through. The monitor is killed only after
// the stop is under way - a command whose monitor is already dead is swept to
// failed by the list that opens the operation, and a swept command is skipped
// rather than stopped.
func TestComposeStop_ReportsTargetFailure(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-stop-fail"
	pidFile := filepath.Join(t.TempDir(), "holder.pid")

	// $$$$ survives compose interpolation as $$, the shell's own pid; exec
	// keeps that pid and carries the ignored SIGTERM disposition with it.
	writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  holder:
    args: [sh, -c, 'echo $$$$ > %s; trap "" TERM; exec sleep 300']
`, project, pidFile))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, stderr, err := env.exec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "up",
	); err != nil {
		t.Fatalf("compose up failed: %v\nstderr:\n%s", err, stderr)
	}

	entries := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(entries) != 1 {
		t.Fatalf("expected one project command, got %d", len(entries))
	}
	id, _ := entries[0]["ID"].(string)
	env.waitForState(ctx, id, "running", defaultTimeout)

	holder := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(holder) })
	monitor := monitorPID(t, env.inspectJSON(ctx, id))

	done := make(chan Result, 1)
	go func() {
		done <- env.Cmd("compose", "--workdir", wd, "-f", composePath, "stop").Exec(ctx)
	}()
	// Long enough for the operation to have listed the project and delivered
	// the ignored SIGTERM, short of the stop's own wait.
	time.Sleep(2 * time.Second)
	killIfAlive(monitor)

	res := <-done
	if res.Err == nil {
		t.Fatalf("compose stop succeeded; expected the target to fail\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "compose stop operation(s) failed") {
		t.Errorf("expected the aggregate stop failure on stderr, got %q", res.Stderr)
	}
	failure := progressError(parseProgress(t, res.Stdout), "holder")
	if failure == "" {
		t.Fatalf("expected holder to reach the error phase; got:\n%s", res.Stdout)
	}
	if !strings.Contains(failure, "timeout waiting for stop") {
		t.Errorf("expected the stop escalation in the reported error, got %q", failure)
	}
}

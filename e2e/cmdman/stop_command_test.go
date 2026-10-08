package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The commands below have a 1 s stop_grace_period. A stop command that runs it
// out is followed by the stop signal, and the SIGKILL comes one grace period
// after that, so a command that ignores both ends about 2 s into the stop. The
// bounds keep that apart from a single grace period and leave room for a
// loaded machine.
const (
	twoGracesMin = 1500 * time.Millisecond
	twoGracesMax = 6 * time.Second
)

// composeStopCommandYAML returns a one-command project whose command runs
// script under sh with a 1 s stop_grace_period and whose stop command runs stop
// under sh. Both go through compose interpolation, so a $ the shell is to see
// is written $$.
func composeStopCommandYAML(project, script, stop string) string {
	return fmt.Sprintf(`name: %s
commands:
  holder:
    args: [sh, -c, %q]
    stop_grace_period: 1s
    stop: [sh, -c, %q]
`, project, script, stop)
}

// upStopCommand brings up a project of composeStopCommandYAML and returns the
// arguments that select the project and the ID of its command once it runs.
func upStopCommand(
	ctx context.Context,
	t *testing.T,
	env *testEnv,
	project, script, stop string,
) (selection []string, id string) {
	t.Helper()
	wd := composeWorkdir(t)
	composePath := writeComposeFile(t, wd, composeStopCommandYAML(project, script, stop))
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

// sleepingStopScript is a stop command that does nothing for the command and
// outlasts any grace period. It records its pid in pidFile first.
func sleepingStopScript(pidFile string) string {
	return fmt.Sprintf(`echo $$$$ > '%s'; exec sleep 300`, pidFile)
}

// TestStopCommand_EndsCommandWithoutSignal pins the stop command running first:
// it gets the command's pid from CMDMAN_MAIN_PID and ends the command, which
// therefore never receives the stop signal.
func TestStopCommand_EndsCommandWithoutSignal(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	dir := t.TempDir()
	var (
		pidFile = filepath.Join(dir, "pid")
		record  = filepath.Join(dir, "record")
		marker  = filepath.Join(dir, "marker")
	)
	// The command ends on USR1 and records a TERM. The stop command waits until
	// the command is gone, so the stop signal would follow only if something
	// were left.
	script := fmt.Sprintf(`trap 'echo TERM >> "%s"; kill $$!; exit 1' TERM
trap 'kill $$!; exit 0' USR1
sleep 300 &
echo $$$$ > '%s'
wait`, record, pidFile)
	stop := fmt.Sprintf(`echo $$CMDMAN_MAIN_PID > '%s'
kill -USR1 $$CMDMAN_MAIN_PID
while kill -0 $$CMDMAN_MAIN_PID 2>/dev/null; do sleep 0.05; done`, marker)

	_, id := upStopCommand(ctx, t, env, "tc-stop-command-ends", script, stop)
	pid := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(pid) })

	env.Cmd("stop", id).Run(ctx, t)

	if state := env.inspectJSON(ctx, id)["State"]; state != "exited" {
		t.Errorf("state after stop = %v, want exited", state)
	}
	if got := strings.TrimSpace(readFile(t, marker)); got != strconv.Itoa(pid) {
		t.Errorf("stop command saw CMDMAN_MAIN_PID %q, want the command's pid %d", got, pid)
	}
	if rec := readFile(t, record); rec != "" {
		t.Errorf("the command received the stop signal: %q", rec)
	}
}

// TestStopCommand_SignalFollowsWhenItRunsOutTheGrace pins the stop signal
// following a stop command that is still running when the grace period runs
// out, and the stop command being killed then.
func TestStopCommand_SignalFollowsWhenItRunsOutTheGrace(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	dir := t.TempDir()
	var (
		pidFile     = filepath.Join(dir, "pid")
		stopPidFile = filepath.Join(dir, "stop.pid")
		marker      = filepath.Join(dir, "marker")
	)
	script := fmt.Sprintf(`trap 'echo TERM > "%s"; kill $$!; exit 0' TERM
sleep 300 &
echo $$$$ > '%s'
wait`, marker, pidFile)

	_, id := upStopCommand(ctx, t, env, "tc-stop-command-grace", script,
		sleepingStopScript(stopPidFile))
	pid := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(pid) })

	res, took := timedExec(ctx, env.Cmd("stop", id))
	if res.Err != nil {
		t.Fatalf("stop failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	stopPid := readPidFile(t, stopPidFile)
	t.Cleanup(func() { killIfAlive(stopPid) })

	assertDuration(t, "stop", took, time.Second, twoGracesMax)
	if strings.TrimSpace(readFile(t, marker)) != "TERM" {
		t.Errorf("the command never received the stop signal")
	}
	waitUntil(t, 5*time.Second, func() bool { return !processExists(stopPid) },
		"stop command pid %d outlived the grace period", stopPid)
}

// TestStopCommand_KillsAfterTwoGracePeriods pins the schedule for a command that
// neither the stop command nor the stop signal ends: the stop command runs out
// one grace period, and the SIGKILL comes one grace period after the signal.
// Neither cmdman stop nor compose stop reports the wait as a failure.
func TestStopCommand_KillsAfterTwoGracePeriods(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"stop", "compose stop"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			ctx := testContext(t)
			env := newTestEnv(t)
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "pid")
			stopPidFile := filepath.Join(dir, "stop.pid")

			selection, id := upStopCommand(
				ctx, t, env, "tc-stop-command-kill",
				termIgnoringScript(pidFile, "$$"), sleepingStopScript(stopPidFile))
			pid := readPidFile(t, pidFile)
			t.Cleanup(func() { killIfAlive(pid) })

			args := []string{"stop", id}
			if verb == "compose stop" {
				args = slices.Concat(selection, []string{"stop"})
			}
			res, took := timedExec(ctx, env.Cmd(args...))
			if res.Err != nil {
				t.Fatalf("%s failed: %v\nstdout:\n%s\nstderr:\n%s",
					verb, res.Err, res.Stdout, res.Stderr)
			}
			assertDuration(t, verb, took, twoGracesMin, twoGracesMax)
			waitUntil(t, 5*time.Second, func() bool { return !processExists(pid) },
				"command pid %d outlived the stop", pid)
		})
	}
}

// TestStopCommand_InterruptedClientKeepsSchedule pins the monitor owning every
// step of the stop: a client killed while the stop command runs changes
// nothing, and the command still ends about two grace periods in.
func TestStopCommand_InterruptedClientKeepsSchedule(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	stopPidFile := filepath.Join(dir, "stop.pid")

	_, id := upStopCommand(ctx, t, env, "tc-stop-command-abandoned",
		termIgnoringScript(pidFile, "$$"), sleepingStopScript(stopPidFile))
	pid := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(pid) })

	client := env.Cmd("stop", id).build(ctx)
	start := time.Now()
	must(t, client.Start())
	t.Cleanup(func() {
		_ = client.Process.Kill()
		_ = client.Wait()
	})

	// The stop command starting shows the stop reached the monitor. Killing the
	// client earlier could kill it before it ever asked for the stop.
	stopPid := readPidFile(t, stopPidFile)
	t.Cleanup(func() { killIfAlive(stopPid) })
	if wait := 200*time.Millisecond - time.Since(start); wait > 0 {
		time.Sleep(wait)
	}
	must(t, client.Process.Kill())
	_ = client.Wait()

	for processExists(pid) {
		if time.Since(start) > 3*twoGracesMax {
			t.Fatalf("command pid %d outlived the stop its client abandoned", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	assertDuration(t, "the abandoned stop", time.Since(start), twoGracesMin, twoGracesMax)
	env.waitForState(ctx, id, "exited", defaultTimeout)
}

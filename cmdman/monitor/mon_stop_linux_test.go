//go:build linux

package monitor

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// A run is not over when its child is reaped: the survivor sweep and the
// output-reader drain still have to finish, and a stop escalating to SIGKILL
// routinely lands in that window. The run keeps the command's process group id
// for exactly that long, so the escalation reaches what the command left behind
// instead of being refused for want of a live child.
func TestMonitorSignalReachesGroupDuringSweep(t *testing.T) {
	// Only what is reparented here can be waited on below, which is how the
	// signal's effect is told apart from a pid that simply went away.
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	survivorPidPath := filepath.Join(dir, "survivor.pid")
	t.Cleanup(func() { killSurvivor(t, survivorPidPath) })

	m, _, _ := newSurvivorMonitor(t, dir, "test-monitor-signal-during-sweep", []string{
		sweepHelperOrphanEnv + "=" + survivorPidPath,
	})

	// Holding the sweep open is what makes the window a test can act in. The
	// real sweep would take the leftover away by itself and prove nothing about
	// the signal, and it would reap it too, which is why the wait below is the
	// test's own.
	sweeping := make(chan struct{})
	release := make(chan struct{})
	m.sweepFn = func(context.Context, *slog.Logger, int) int {
		close(sweeping)
		<-release
		return 0
	}

	// The run's error comes back to the test goroutine: a failed assertion calls
	// FailNow, which only the test goroutine may do.
	runErr := make(chan error, 1)
	go func() {
		_, err := m.runOnce(t.Context())
		runErr <- err
	}()

	select {
	case <-sweeping:
	case <-time.After(30 * time.Second):
		t.Fatal("the run never reached its sweep")
	}
	// Registered once the sweep is known to be held, so a failed assertion below
	// still lets the run finish instead of leaving it parked for good.
	releaseSweep := sync.OnceFunc(func() { close(release) })
	defer releaseSweep()

	// Without this the assertions below would pass on the live-child path and
	// say nothing about a run that has already given its handles up.
	_, _, pid := m.GetState()
	assert.Equal(t, pid, 0, "the run still reports a live process")

	survivor, ok := readPidFile(t, survivorPidPath)
	assert.Assert(t, ok, "the command never reported the pid it left behind")
	stat, found := procStatOf(t, survivor)
	assert.Assert(t, found && stat.state != "Z", "the leftover was gone before the signal")

	assert.NilError(t, m.SignalProcess(syscall.SIGKILL))
	assert.Assert(
		t,
		len(waitGone(t.Context(), []int{survivor}, 10*time.Second)) == 0,
		"the signal never reached the process the command left behind",
	)

	releaseSweep()
	select {
	case err := <-runErr:
		assert.NilError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the run never ended")
	}

	// With the run over there is no group left to aim at, so the next caller is
	// told so rather than signalling a pid that has been handed out again.
	assert.ErrorIs(t, m.SignalProcess(syscall.SIGKILL), errNoRunningProcess)
}

// A stop with a timeout escalates on the monitor's own clock. The client that
// asked for it may never come back to send SIGKILL, so a command ignoring the
// stop signal is killed once the timeout expires by the one stop alone.
func TestMonitorStopDeadlineKillsCommandIgnoringSignal(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(
		t, dir, "test-monitor-stop-deadline",
		`trap "" TERM; : >"$1"; sleep 300`, readyPath,
	)

	done := startRun(t, m)
	awaitCommandReady(t, m, readyPath)

	const timeout = 300 * time.Millisecond
	stoppedAt := time.Now()
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, timeout))

	// The run's context stays live and the test never signals the command
	// again, so the deadline is the only SIGKILL there is.
	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1, "the command was not killed")
	assert.Assert(
		t,
		time.Since(stoppedAt) >= timeout,
		"the command ended ahead of the deadline, so the stop signal took it",
	)
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
}

// The deadline reaches what the command left behind as well. A stop landing
// while the run winds down finds no live child, and a leftover that ignores
// the stop signal is taken by the deadline through the group the run keeps.
func TestMonitorStopDeadlineReachesWhatTheCommandLeftBehind(t *testing.T) {
	// The leftover is reparented here once the shell exits, which is what lets
	// the wait below reap it.
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	survivorPidPath := filepath.Join(dir, "survivor.pid")
	t.Cleanup(func() { killSurvivor(t, survivorPidPath) })

	m := newShellMonitor(
		t, dir, "test-monitor-stop-deadline-survivor",
		`trap "" TERM; sleep 300 </dev/null >/dev/null 2>&1 & echo $! >"$1"`, survivorPidPath,
	)

	// The real sweep would take the leftover away by itself, so it is held open
	// for the deadline to be what acts on it.
	sweeping := make(chan struct{})
	release := make(chan struct{})
	m.sweepFn = func(context.Context, *slog.Logger, int) int {
		close(sweeping)
		<-release
		return 0
	}

	done := startRun(t, m)
	select {
	case <-sweeping:
	case <-time.After(30 * time.Second):
		t.Fatal("the run never reached its sweep")
	}
	releaseSweep := sync.OnceFunc(func() { close(release) })
	defer releaseSweep()

	_, _, pid := m.GetState()
	assert.Equal(t, pid, 0, "the run still reports a live process")

	survivor, ok := readPidFile(t, survivorPidPath)
	assert.Assert(t, ok, "the command never reported the pid it left behind")
	stat, found := procStatOf(t, survivor)
	assert.Assert(t, found && stat.state != "Z", "the leftover was gone before the stop")

	const timeout = 300 * time.Millisecond
	stoppedAt := time.Now()
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, timeout))
	assert.Assert(
		t,
		len(waitGone(t.Context(), []int{survivor}, 10*time.Second)) == 0,
		"the deadline never reached the process the command left behind",
	)
	assert.Assert(
		t,
		time.Since(stoppedAt) >= timeout,
		"the leftover went ahead of the deadline, so the stop signal took it",
	)

	releaseSweep()
	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
}

// A stop without a timeout leaves escalation to the client. The monitor must
// not make one up: a command ignoring the stop signal keeps running.
func TestMonitorStopWithoutTimeoutNeverEscalates(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(
		t, dir, "test-monitor-stop-no-deadline",
		`trap "" TERM; : >"$1"; sleep 300`, readyPath,
	)

	done := startRun(t, m)
	pid := awaitCommandReady(t, m, readyPath)

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 0))
	assert.Assert(t, armedStopDeadline(m) == nil, "a stop without a timeout armed a deadline")

	// Several times the deadline the tests above use.
	select {
	case got := <-done:
		t.Fatalf(
			"the run ended (code %d, err %v) with nothing escalating the stop",
			got.code,
			got.err,
		)
	case <-time.After(1500 * time.Millisecond):
	}
	_, _, livePid := m.GetState()
	assert.Equal(t, livePid, pid, "the command is no longer running")

	assert.NilError(t, m.SignalProcess(syscall.SIGKILL))
	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
}

// A deadline must not outlive its run: past the run's end the group it would
// aim at can be handed out again. The run stops it where it gives the group up.
func TestMonitorRunEndDisarmsStopDeadline(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(
		t,
		dir,
		"test-monitor-stop-deadline-disarm",
		`: >"$1"; sleep 300`,
		readyPath,
	)

	done := startRun(t, m)
	awaitCommandReady(t, m, readyPath)

	// The stop signal takes this command at once, and the deadline is far enough
	// out that only the run's end can have stopped it.
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, time.Minute))
	armed := armedStopDeadline(m)
	assert.Assert(t, armed != nil, "the stop armed no deadline")

	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
	// Stop reports whether it was the one to stop the timer. A minute has not
	// passed, so false means the run stopped it rather than merely dropping it.
	assert.Assert(t, !armed.Stop(), "the run let go of the deadline without stopping it")
}

// newShellMonitor supervises script under /bin/sh in place of the helper
// process newSurvivorMonitor wires: the helper's leftovers take SIGTERM's
// default action, and a shell's trap is what hands a process that ignores it.
// runOnce runs the command m.cfg names, so swapping the argv in memory is
// enough for it. runLoop re-reads the config from disk and would run the helper
// instead. args become the script's positional parameters.
func newShellMonitor(t *testing.T, dir, id, script string, args ...string) *Monitor {
	t.Helper()
	m, _, _ := newSurvivorMonitor(t, dir, id, nil)
	m.cfg.Argv = append([]string{"/bin/sh", "-c", script, "sh"}, args...)
	return m
}

type runOutcome struct {
	code int
	err  error
}

// startRun runs the command once on a goroutine of its own so the test can act
// on the run while it goes. A test that fails before the run ends would leave
// a command that ignores SIGTERM running, so cleanup kills whatever is left.
func startRun(t *testing.T, m *Monitor) <-chan runOutcome {
	t.Helper()
	done := make(chan runOutcome, 1)
	go func() {
		code, err := m.runOnce(t.Context())
		done <- runOutcome{code: code, err: err}
	}()
	t.Cleanup(func() { _ = m.SignalProcess(syscall.SIGKILL) })
	return done
}

// awaitRun returns the exit code of the run startRun began. It runs on the test
// goroutine because a failed assertion calls FailNow, which only the test
// goroutine may do.
func awaitRun(t *testing.T, done <-chan runOutcome, timeout time.Duration) int {
	t.Helper()
	select {
	case got := <-done:
		assert.NilError(t, got.err)
		return got.code
	case <-time.After(timeout):
		t.Fatal("the run never ended")
		return -1
	}
}

// awaitCommandReady waits until the run has published its child and the
// script has written readyPath, and returns the child's pid. The scripts write
// it once their trap is in place: a stop landing before that would take the
// shell outright and say nothing about the deadline.
func awaitCommandReady(t *testing.T, m *Monitor, readyPath string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, _, pid := m.GetState()
		if _, err := os.Stat(readyPath); err == nil && pid != 0 {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func armedStopDeadline(m *Monitor) *time.Timer {
	m.procMu.Lock()
	defer m.procMu.Unlock()
	return m.stopDeadline
}

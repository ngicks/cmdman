//go:build linux

package monitor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"gotest.tools/v3/assert"
)

// trappingChildScript records a TERM in $1 and exits on it, and has written $2
// once its trap is in place. Its sleep sits in the command's process group, so
// a stop signal to the group ends it as well.
const trappingChildScript = `trap 'echo TERM >>"$1"; exit 0' TERM
: >"$2"
sleep 300 &
wait`

// stopFiles are the files the scripts of a stop command test hand off
// through. Both scripts get them as $1 to $5, in field order.
type stopFiles struct {
	record   string
	ready    string
	gate     string
	pid      string
	childPid string
}

func newStopFiles(dir string) stopFiles {
	return stopFiles{
		record:   filepath.Join(dir, "record"),
		ready:    filepath.Join(dir, "ready"),
		gate:     filepath.Join(dir, "gate"),
		pid:      filepath.Join(dir, "stop.pid"),
		childPid: filepath.Join(dir, "stop-child.pid"),
	}
}

func (f stopFiles) args() []string {
	return []string{f.record, f.ready, f.gate, f.pid, f.childPid}
}

// newStopCommandMonitor is newShellMonitor for a command whose stop command is
// stopScript under /bin/sh. Both scripts get files as their positional
// parameters.
func newStopCommandMonitor(
	t *testing.T,
	dir, id, script, stopScript string,
	files stopFiles,
) *Monitor {
	t.Helper()
	m := newShellMonitor(t, dir, id, script, files.args()...)
	m.cfg.StopCommand = &model.StopCommand{
		Args: append([]string{"/bin/sh", "-c", stopScript, "sh"}, files.args()...),
	}
	return m
}

// stopWithin calls StopProcess on a goroutine of its own and fails the test
// when it does not return within a few seconds: a stop that waited for its stop
// command would hold the RPC for as long as that runs.
func stopWithin(t *testing.T, m *Monitor, sig syscall.Signal, timeout time.Duration) {
	t.Helper()
	stopped := make(chan error, 1)
	go func() { stopped <- m.StopProcess(sig, timeout) }()
	select {
	case err := <-stopped:
		assert.NilError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatalf("StopProcess(%s) did not return", sig)
	}
}

func readRecord(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	assert.NilError(t, err)
	return string(b)
}

// waitPidFile waits until path carries a pid and returns it.
func waitPidFile(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitUntil(t, 10*time.Second, func() bool {
		var ok bool
		pid, ok = readPidFile(t, path)
		return ok
	}, "%s never carried a pid", path)
	return pid
}

func currentStopSequence(m *Monitor) *stopSequence {
	m.procMu.Lock()
	defer m.procMu.Unlock()
	return m.stopSeq
}

// A stop of a command with a stop command returns at once and leaves the
// command alone while the stop command runs. The stop signal follows only once
// the stop command is done, and the stop command is told the pid of the
// command's own process.
func TestMonitorStopCommandRunsBeforeTheSignal(t *testing.T) {
	dir := t.TempDir()
	files := newStopFiles(dir)
	m := newStopCommandMonitor(
		t, dir, "test-monitor-stop-command-first",
		trappingChildScript,
		`while [ ! -e "$3" ]; do sleep 0.02; done; echo "stop $CMDMAN_MAIN_PID" >>"$1"`,
		files,
	)

	done := startRun(t, m)
	pid := awaitCommandReady(t, m, files.ready)

	stopWithin(t, m, syscall.SIGTERM, time.Minute)
	assert.Assert(
		t,
		armedStopDeadline(m) == nil,
		"the stop armed its deadline ahead of the stop command",
	)
	select {
	case got := <-done:
		t.Fatalf("the run ended (code %d, err %v) while the stop command ran", got.code, got.err)
	case <-time.After(300 * time.Millisecond):
	}
	assert.Equal(t, readRecord(t, files.record), "", "the command was signalled first")

	assert.NilError(t, os.WriteFile(files.gate, nil, 0o600))
	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)

	assert.Equal(t, readRecord(t, files.record), "stop "+strconv.Itoa(pid)+"\nTERM\n")
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
	assert.Assert(t, currentStopSequence(m) == nil, "the run ended with its stop sequence")
}

// A stop command that ends the command itself leaves the stop nothing to
// signal, and the run's process group gets no stop signal.
func TestMonitorStopCommandLeavesNothingToSignal(t *testing.T) {
	dir := t.TempDir()
	files := newStopFiles(dir)
	m := newStopCommandMonitor(
		t, dir, "test-monitor-stop-command-ends-command",
		`: >"$2"; exec sleep 300`,
		`kill -KILL "$CMDMAN_MAIN_PID"
while kill -0 "$CMDMAN_MAIN_PID" 2>/dev/null; do sleep 0.02; done`,
		files,
	)
	rec := &killRecorder{deliver: true}
	m.stopSignalFn = func(pgid int, sig syscall.Signal) error { return rec.kill(-pgid, sig) }

	done := startRun(t, m)
	awaitCommandReady(t, m, files.ready)

	stopWithin(t, m, syscall.SIGTERM, time.Minute)
	assert.Equal(t, awaitRun(t, done, 30*time.Second), -1, "the stop command did not kill it")

	assert.Assert(t, len(rec.calls()) == 0, "the stop signal went out: %v", rec.calls())
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
}

// A stop command that runs out the grace period is killed together with what it
// started, and the stop signal follows.
func TestMonitorStopCommandKilledAtGrace(t *testing.T) {
	// What the stop command started is reparented here once the stop command is
	// killed, which is what lets the test see it reaped rather than gone to init.
	becomeSubreaperForTest(t)

	dir := t.TempDir()
	files := newStopFiles(dir)
	m := newStopCommandMonitor(
		t, dir, "test-monitor-stop-command-grace",
		trappingChildScript,
		`sleep 300 & echo $! >"$5"; echo $$ >"$4"; wait`,
		files,
	)
	t.Cleanup(func() {
		killPidFile(t, files.pid)
		killPidFile(t, files.childPid)
	})

	done := startRun(t, m)
	awaitCommandReady(t, m, files.ready)

	const grace = 500 * time.Millisecond
	stoppedAt := time.Now()
	stopWithin(t, m, syscall.SIGTERM, grace)
	leader := waitPidFile(t, files.pid)
	child := waitPidFile(t, files.childPid)

	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)
	assert.Assert(
		t,
		time.Since(stoppedAt) >= grace,
		"the command ended ahead of the grace period, so the stop command was not waited for",
	)
	assert.Equal(t, readRecord(t, files.record), "TERM\n")
	assert.Assert(t, survivorGone(t, leader), "the stop command outlived its grace period")
	assert.Assert(t, survivorGone(t, child), "what the stop command started outlived it")
}

// SIGKILL does not wait for a stop command: it goes out at once, the stop
// command is killed, and no stop signal follows.
func TestMonitorStopKillDuringStopCommand(t *testing.T) {
	dir := t.TempDir()
	files := newStopFiles(dir)
	m := newStopCommandMonitor(
		t, dir, "test-monitor-stop-command-sigkill",
		trappingChildScript,
		`echo $$ >"$4"; exec sleep 300`,
		files,
	)
	t.Cleanup(func() { killPidFile(t, files.pid) })
	rec := &killRecorder{deliver: true}
	m.stopSignalFn = func(pgid int, sig syscall.Signal) error { return rec.kill(-pgid, sig) }

	done := startRun(t, m)
	awaitCommandReady(t, m, files.ready)

	stopWithin(t, m, syscall.SIGTERM, time.Minute)
	stopCommand := waitPidFile(t, files.pid)

	stopWithin(t, m, syscall.SIGKILL, 0)
	// Far short of the minute the stop command had.
	assert.Equal(t, awaitRun(t, done, 30*time.Second), -1)

	assert.Equal(t, readRecord(t, files.record), "", "the command was signalled gracefully")
	assert.Assert(t, len(rec.calls()) == 0, "the stop signal went out: %v", rec.calls())
	assert.Assert(t, survivorGone(t, stopCommand), "the stop command outlived the SIGKILL")
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
}

// A graceful stop that finds a stop in progress changes nothing: the stop
// command does not run again, its signal is not sent, and its timeout arms no
// deadline.
func TestMonitorSecondStopDuringStopCommandChangesNothing(t *testing.T) {
	dir := t.TempDir()
	files := newStopFiles(dir)
	m := newStopCommandMonitor(
		t, dir, "test-monitor-stop-command-second-stop",
		`trap 'echo USR1 >>"$1"; exit 0' USR1
`+trappingChildScript,
		`echo stop >>"$1"; while [ ! -e "$3" ]; do sleep 0.02; done`,
		files,
	)

	done := startRun(t, m)
	awaitCommandReady(t, m, files.ready)

	stopWithin(t, m, syscall.SIGTERM, time.Minute)
	waitUntil(t, 10*time.Second, func() bool {
		return readRecord(t, files.record) == "stop\n"
	}, "the stop command never ran")

	// A deadline this short would kill the command long before the gate opens.
	stopWithin(t, m, syscall.SIGUSR1, 50*time.Millisecond)
	assert.Assert(t, armedStopDeadline(m) == nil, "the second stop armed a deadline")
	select {
	case got := <-done:
		t.Fatalf("the run ended (code %d, err %v) on the second stop", got.code, got.err)
	case <-time.After(300 * time.Millisecond):
	}
	assert.Equal(t, readRecord(t, files.record), "stop\n", "the second stop did something")

	assert.NilError(t, os.WriteFile(files.gate, nil, 0o600))
	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)
	assert.Equal(t, readRecord(t, files.record), "stop\nTERM\n")
}

// Without a stop command a stop signals the command and arms its deadline
// itself, as it always has. With one, both wait for the stop command.
func TestMonitorStopArmsDeadlineAtOnceOnlyWithoutStopCommand(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stopCommand bool
	}{
		{name: "without stop command"},
		{name: "with stop command", stopCommand: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := newStopFiles(dir)
			m := newStopCommandMonitor(
				t, dir, "test-monitor-stop-deadline-path",
				`trap "" TERM; : >"$2"; sleep 300`,
				`while [ ! -e "$3" ]; do sleep 0.02; done`,
				files,
			)
			if !tc.stopCommand {
				m.cfg.StopCommand = nil
			}

			done := startRun(t, m)
			awaitCommandReady(t, m, files.ready)

			stopWithin(t, m, syscall.SIGTERM, time.Minute)
			armed := armedStopDeadline(m) != nil
			assert.Equal(t, armed, !tc.stopCommand, "deadline armed by the stop itself: %t", armed)

			assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))
			assert.Equal(t, awaitRun(t, done, 30*time.Second), -1)
		})
	}
}

// The run does not give its process group up while a stop command a stop began
// is still going, even when the command is already gone: the stop command is
// let finish, and nothing it armed outlives the run.
func TestMonitorRunEndWaitsForStopCommand(t *testing.T) {
	dir := t.TempDir()
	files := newStopFiles(dir)
	m := newStopCommandMonitor(
		t, dir, "test-monitor-stop-command-run-end",
		`: >"$2"; exec sleep 300`,
		`kill -KILL "$CMDMAN_MAIN_PID"; sleep 0.5; echo done >>"$1"`,
		files,
	)

	done := startRun(t, m)
	awaitCommandReady(t, m, files.ready)

	stopWithin(t, m, syscall.SIGTERM, time.Minute)
	assert.Equal(t, awaitRun(t, done, 30*time.Second), -1)

	assert.Equal(
		t,
		readRecord(t, files.record),
		"done\n",
		"the run ended ahead of its stop command",
	)
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
	assert.Assert(t, currentStopSequence(m) == nil, "the run ended with its stop sequence")
}

// A monitor shutting down kills a stop command that is still running instead of
// waiting out its grace period.
func TestMonitorShutdownKillsStopCommand(t *testing.T) {
	dir := t.TempDir()
	files := newStopFiles(dir)
	m := newStopCommandMonitor(
		t, dir, "test-monitor-stop-command-shutdown",
		`: >"$2"; exec sleep 300`,
		`echo $$ >"$4"; exec sleep 300`,
		files,
	)
	t.Cleanup(func() { killPidFile(t, files.pid) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan runOutcome, 1)
	go func() {
		code, err := m.runOnce(ctx)
		done <- runOutcome{code: code, err: err}
	}()
	t.Cleanup(func() { _ = m.SignalProcess(syscall.SIGKILL) })
	awaitCommandReady(t, m, files.ready)

	stopWithin(t, m, syscall.SIGTERM, time.Minute)
	stopCommand := waitPidFile(t, files.pid)

	cancel()
	awaitRun(t, done, 30*time.Second)
	assert.Assert(t, survivorGone(t, stopCommand), "the stop command outlived the shutdown")
}

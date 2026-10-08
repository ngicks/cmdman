//go:build linux

package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"gotest.tools/v3/assert"
)

// termIgnoringScript ignores SIGTERM, and its sleep inherits that, so only
// SIGKILL ends it. It writes $1 once the trap is in place.
const termIgnoringScript = `trap "" TERM; : >"$1"; sleep 300`

// The deadline a graceful stop armed is what ended the run, and the run says so
// once: one stopped event with the reason, the anomaly on the exited event, and
// the flag and the warning in the persisted state.
func TestMonitorStopDeadlineRecordsForcedKill(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(t, dir, "test-monitor-force-kill-deadline", termIgnoringScript, readyPath)

	done := startRun(t, m)
	awaitCommandReady(t, m, readyPath)

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 300*time.Millisecond))
	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
	m.setExited(-1)

	assertForcedKillRecorded(t, m)
}

// The client sends a SIGKILL of its own once its wait is over, at about the
// deadline the monitor armed. Whichever comes second finds the forced kill
// recorded already and adds nothing.
func TestMonitorClientEscalationAfterDeadlineAddsNothing(t *testing.T) {
	// The killed sleep is reparented here and left unreaped while the sweep is
	// held, so the client's SIGKILL still finds a member of the group.
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	pidPath := filepath.Join(dir, "sleep.pid")
	t.Cleanup(func() { killSurvivor(t, pidPath) })
	m := newShellMonitor(
		t, dir, "test-monitor-force-kill-client-dup",
		`trap "" TERM; sleep 300 </dev/null >/dev/null 2>&1 & echo $! >"$1"; wait`, pidPath,
	)

	awaiting, release := holdAwait(t, m)

	done := startRun(t, m)
	awaitCommandReady(t, m, pidPath)

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 300*time.Millisecond))
	awaitClosed(t, awaiting, "the deadline never ended the command")
	assert.Assert(t, m.forceKilled.Load(), "the deadline recorded no forced kill")

	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))

	release()
	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
	m.setExited(-1)

	assertForcedKillRecorded(t, m)
}

// A graceful stop without a timeout arms no deadline and leaves the SIGKILL to
// its client. That SIGKILL ends a stop that ran out of patience the same way
// the deadline does.
func TestMonitorClientEscalationRecordsForcedKill(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(t, dir, "test-monitor-force-kill-client", termIgnoringScript, readyPath)

	done := startRun(t, m)
	awaitCommandReady(t, m, readyPath)

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 0))
	assert.Assert(t, !m.forceKilled.Load(), "the graceful stop itself recorded a forced kill")

	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))
	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
	m.setExited(-1)

	assertForcedKillRecorded(t, m)
}

// A SIGKILL asked for in its own right is no stop that ran out its grace
// period: neither a SIGKILL stop nor a bare SIGKILL signal records a forced
// kill, and a bare one does not even while a graceful stop is in progress.
func TestMonitorExplicitSigkillRecordsNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		kill func(m *Monitor) error
	}{
		{name: "stop", kill: func(m *Monitor) error {
			return m.StopProcess(syscall.SIGKILL, 0)
		}},
		{name: "signal", kill: func(m *Monitor) error {
			return m.SignalProcess(syscall.SIGKILL)
		}},
		{name: "signal during graceful stop", kill: func(m *Monitor) error {
			if err := m.StopProcess(syscall.SIGTERM, 0); err != nil {
				return err
			}
			return m.SignalProcess(syscall.SIGKILL)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			readyPath := filepath.Join(dir, "ready")
			m := newShellMonitor(
				t, dir, "test-monitor-force-kill-explicit", termIgnoringScript, readyPath)

			done := startRun(t, m)
			awaitCommandReady(t, m, readyPath)

			assert.NilError(t, tc.kill(m))
			assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
			m.setExited(-1)

			assertNoForcedKill(t, m)
		})
	}
}

// A SIGKILL that finds nothing of the run left kills nothing, whether the run
// is still winding down with an empty process group or already over.
func TestMonitorEscalationWithNothingLeftRecordsNothing(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(
		t, dir, "test-monitor-force-kill-nothing-left", `: >"$1"; exec sleep 300`, readyPath)

	awaiting, release := holdAwait(t, m)

	done := startRun(t, m)
	awaitCommandReady(t, m, readyPath)

	// sleep dies of the stop signal, and the deadline is far enough out that
	// only the SIGKILLs below can be the escalation.
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, time.Minute))
	awaitClosed(t, awaiting, "the stop signal never ended the command")

	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))
	release()
	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))
	m.setExited(-1)

	assertNoForcedKill(t, m)
}

// holdAwait holds the run of m at the end of a stopped run, once the child is
// reaped and while the run still keeps its process group, until release is
// called. awaiting is closed once the run gets there. A test that fails first
// releases it on cleanup.
func holdAwait(t *testing.T, m *Monitor) (awaiting <-chan struct{}, release func()) {
	t.Helper()
	reached := make(chan struct{})
	released := make(chan struct{})
	m.awaitFn = func(context.Context, *slog.Logger, int, func() bool) int {
		close(reached)
		<-released
		return 0
	}
	release = sync.OnceFunc(func() { close(released) })
	t.Cleanup(release)
	return reached, release
}

// assertForcedKillRecorded checks everything the run m ended with says it was
// force-killed, and that the event saying so is there once.
func assertForcedKillRecorded(t *testing.T, m *Monitor) {
	t.Helper()
	path := eventLogPath(t, m)

	forced := forcedKillEvents(t, path)
	assert.Equal(t, len(forced), 1, "forced-kill events: %v", forced)
	assert.Equal(t, forced[0].Attrs["signal"], strconv.Itoa(int(syscall.SIGKILL)))

	exited := lastEventOfType(t, path, model.EventTypeExited)
	assert.Equal(t, exited.Attrs["force_killed"], "true")

	_, _, stateJSON, err := m.store.GetCommandState(m.ID)
	assert.NilError(t, err)
	assert.Assert(t, stateJSON.ForceKilled, "the state does not say the run was force-killed")
	assert.DeepEqual(t, stateJSON.Warnings, []string{anomalyForceKilled.msg})
}

// assertNoForcedKill checks nothing the run m ended with says it was
// force-killed.
func assertNoForcedKill(t *testing.T, m *Monitor) {
	t.Helper()
	path := eventLogPath(t, m)

	stopped := eventsOfType(t, path, model.EventTypeStopped)
	assert.Equal(t, len(stopped), 0, "the monitor recorded stops: %v", stopped)

	exited := lastEventOfType(t, path, model.EventTypeExited)
	_, ok := exited.Attrs["force_killed"]
	assert.Assert(t, !ok, "the exited event says force_killed: %v", exited.Attrs)

	_, _, stateJSON, err := m.store.GetCommandState(m.ID)
	assert.NilError(t, err)
	assert.Assert(t, !stateJSON.ForceKilled, "the state says the run was force-killed")
	assert.Assert(t, len(stateJSON.Warnings) == 0, "the state warns: %v", stateJSON.Warnings)
}

func eventLogPath(t *testing.T, m *Monitor) string {
	t.Helper()
	path, err := m.Config.EventLogPath()
	assert.NilError(t, err)
	return path
}

// forcedKillEvents returns the stopped events the log at path records for a
// stop that ran out its grace period.
func forcedKillEvents(t *testing.T, path string) []model.Event {
	t.Helper()
	var out []model.Event
	for _, e := range eventsOfType(t, path, model.EventTypeStopped) {
		if e.Attrs["reason"] == "timeout" {
			out = append(out, e)
		}
	}
	return out
}

// eventsOfType returns every event of type typ the log at path holds, in
// order.
func eventsOfType(t *testing.T, path string, typ model.EventType) []model.Event {
	t.Helper()
	f, err := os.Open(path)
	assert.NilError(t, err)
	defer f.Close()

	var out []model.Event
	dec := json.NewDecoder(f)
	for {
		var e model.Event
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			return out
		}
		assert.NilError(t, err)
		if e.Type == typ {
			out = append(out, e)
		}
	}
}

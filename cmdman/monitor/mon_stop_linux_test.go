//go:build linux

package monitor

import (
	"context"
	"log/slog"
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

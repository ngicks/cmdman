package monitor

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"gotest.tools/v3/assert"
)

// A stop can land when there is nothing left to signal: between one run's end
// and the restart that follows. It still has to succeed, because the restart
// suppression it latched is what ends the loop. A bare signal in the same state
// keeps saying it reached nothing.
func TestMonitorStopWithoutProcessSucceeds(t *testing.T) {
	var m Monitor

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 0))
	assert.Assert(t, m.stopRequested.Load(), "the stop did not suppress restarts")

	assert.ErrorIs(t, m.SignalProcess(syscall.SIGTERM), errNoRunningProcess)
}

// Each stop replaces the deadline an earlier one armed, so there is never more
// than one SIGKILL pending. SIGKILL has nothing left to escalate to and arms
// none.
func TestMonitorStopReplacesDeadline(t *testing.T) {
	var m Monitor
	deadline := func() *time.Timer {
		m.procMu.Lock()
		defer m.procMu.Unlock()
		return m.stopDeadline
	}

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, time.Minute))
	first := deadline()
	assert.Assert(t, first != nil, "the stop armed no deadline")

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, time.Minute))
	second := deadline()
	assert.Assert(
		t,
		second != nil && second != first,
		"the second stop armed no deadline of its own",
	)
	// Stop reports whether it was the one to stop the timer, so false means the
	// second stop already had.
	assert.Assert(t, !first.Stop(), "the earlier deadline is still pending")

	assert.NilError(t, m.StopProcess(syscall.SIGKILL, time.Minute))
	assert.Assert(t, deadline() == nil, "SIGKILL armed a deadline")
	assert.Assert(t, !second.Stop(), "the deadline SIGKILL superseded is still pending")
}

// A command that exits at once leaves an empty process group behind, so a stop
// arriving while the run winds down signals a group nobody is in. The restart
// policy still has to honour it: the loop ends after the run it landed on
// instead of starting another one, and the caller is told the stop worked.
func TestMonitorStopDuringRunEndEndsRestartLoop(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-stop-between-restarts"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "exit 0"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyAlways,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m, err := newMonitor(t.Context(), id, appCfg, logger)
	assert.NilError(t, err)
	defer m.Close()

	// The sweep is the one point a test can name inside the window between a
	// child being reaped and its run being over, which is where the stop has to
	// land for this to be about anything.
	stopErr := errors.New("the sweep never ran, so no stop was ever attempted")
	stopped := false
	m.sweepFn = func(context.Context, *slog.Logger, int, func() bool) int {
		if !stopped {
			stopped = true
			stopErr = m.StopProcess(syscall.SIGTERM, 0)
		}
		return 0
	}

	// The loop's error comes back to the test goroutine: a failed assertion
	// calls FailNow, which only the test goroutine may do. Everything the sweep
	// recorded is published to this goroutine by the same send.
	loopErr := make(chan error, 1)
	go func() { loopErr <- m.runLoop(t.Context()) }()

	select {
	case err := <-loopErr:
		assert.NilError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the always-restart loop never ended, so the stop did not take")
	}

	assert.NilError(t, stopErr)

	history, err := st.GetExitHistory(id)
	assert.NilError(t, err)
	assert.Equal(t, len(history), 1, "the loop restarted after the stop")

	state, _, stateJSON, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeExited)
	// The policy wanted another run and the stop is what denied it, so the
	// restart that never happened must not show up in the count either.
	assert.Equal(t, stateJSON.RestartCount, 0, "a cancelled restart was counted as one")
}

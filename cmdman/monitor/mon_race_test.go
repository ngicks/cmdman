package monitor

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

// Clients call Status, WriteStdin and Signal whenever they like, including the
// instant a run ends. The monitor drops the run's process handles as soon as it
// has reaped the child, so those three calls read the handles while the run
// replaces them. Driving all three across a run's end under -race is what
// reports an unguarded read of the handles.
//
// Each call gets its own goroutine: a command that never drains its stdin
// eventually blocks the stdin writer on a full pipe, and sharing a goroutine
// would stall the other two calls behind it.
func TestMonitorRPCsAcrossRunEnd(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-rpc-across-run-end"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "sleep 1"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
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

	// The PID a Status call reports comes from the run's own handles, so a
	// caller that saw a PID and later saw none has demonstrably crossed the
	// point where the run gave those handles up.
	var sawPID, sawPIDGone atomic.Bool

	var hammers errgroup.Group
	hammerCtx, stopHammering := context.WithCancel(t.Context())
	// Registered after m.Close so it runs first: a failed assertion below must
	// not tear the monitor down while callers are still in flight.
	defer func() {
		stopHammering()
		_ = hammers.Wait()
	}()

	hammers.Go(func() error {
		for hammerCtx.Err() == nil {
			_, _, pid := m.GetState()
			switch {
			case pid != 0:
				sawPID.Store(true)
			case sawPID.Load():
				sawPIDGone.Store(true)
			}
		}
		return nil
	})
	hammers.Go(func() error {
		for hammerCtx.Err() == nil {
			// Signal 0 delivers nothing, so the run ends on its own schedule
			// rather than on this call's. A call landing after the run ended
			// reports an error, which is the case under test.
			_ = m.SignalProcess(syscall.Signal(0))
		}
		return nil
	})
	hammers.Go(func() error {
		for hammerCtx.Err() == nil {
			_ = m.QueueStdin(hammerCtx, []byte("x"))
		}
		return nil
	})

	// The run's error comes back to the test goroutine: a failed assertion calls
	// FailNow, which only the test goroutine may do.
	runErr := make(chan error, 1)
	go func() {
		_, err := m.runOnce(t.Context())
		runErr <- err
	}()

	select {
	case err := <-runErr:
		assert.NilError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the run never finished")
	}

	waitUntil(t, 10*time.Second, sawPIDGone.Load, "no call landed on both sides of the run's end")

	stopHammering()
	assert.NilError(t, hammers.Wait())
}

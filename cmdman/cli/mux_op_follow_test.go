package cli

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
)

// startInProcessMonitor stands in for the detached start: a test binary cannot
// re-exec itself as a monitor, so one runs here instead. It waits the way the
// real start path does — until the command is observably running, or already
// finished and gone — so what follows it sees the same thing either way.
func startInProcessMonitor(
	t *testing.T,
	cfg cmdman.CmdmanConfig,
	svc *cmdman.Service,
) (func(context.Context, string) error, <-chan error) {
	t.Helper()

	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	t.Cleanup(stopMonitor)
	done := make(chan error, 1)

	return func(ctx context.Context, id string) error {
		go func() {
			done <- monitor.RunMonitor(monitorCtx, id, cfg, slog.New(
				slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}),
			))
		}()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			entries, err := svc.List(ctx, cmdman.ListRequest{AllStates: true})
			if err != nil {
				return err
			}
			found := false
			for _, e := range entries {
				if e.ID != id {
					continue
				}
				found = true
				if e.State == model.EventTypeRunning {
					return nil
				}
			}
			if !found {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
		}
		return errors.New("timed out waiting for the in-process monitor")
	}, done
}

// The worker's record is erased the moment it exits: auto-remove takes its
// state, its exit code and its exit history with it. What the operation
// reported has to survive that, so this drives a real monitor over a command
// registered exactly the way a mux operation is, and pins that the status still
// comes back — along with what the worker printed, both as it happened and
// afterwards.
func TestRunMuxOpWorker(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	defer svc.Close()

	var out bytes.Buffer
	opts := muxOpTestOptions(t, svc)
	opts.Stdout = &out
	opts.Stderr = &out

	req, err := muxOpCreateRequest(opts)
	assert.NilError(t, err)
	req.Argv = []string{"/bin/sh", "-c", "echo applied; exit 3"}

	start, monitorDone := startInProcessMonitor(t, cfg, svc)
	err = runMuxOpWorker(t.Context(), opts, req, start)

	exitErr, ok := errors.AsType[*ExitCodeError](err)
	assert.Assert(t, ok, "got %v", err)
	assert.Equal(t, exitErr.Code, 3)
	assert.Assert(t, strings.Contains(out.String(), "applied"), "output: %q", out.String())

	select {
	case err := <-monitorDone:
		assert.NilError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the monitor to finish")
	}

	entry, err := findCommandByName(t.Context(), svc, req.Name)
	assert.NilError(t, err)
	assert.Assert(t, entry == nil, "the record outlived the operation")

	// The log lives in the runtime dir rather than the removed command dir, so
	// there is still something to read after the record is gone.
	logged, err := os.ReadFile(req.LogOpts[logdriver.LogOptPath])
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(logged), "applied"), "log: %q", logged)
}

// A worker that finishes before following starts takes its record with it, and
// the follow plumbing reads that record. Replaying the log file is what is left,
// and it has to reach the same place the live stream would have.
func TestReplayMuxOpLog(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	defer svc.Close()

	logPath, err := muxOpLogPath(cfg, "replay")
	assert.NilError(t, err)

	// Two separate writers over one path: an identity can be operated on again,
	// and the second run must add to the record rather than erase the first.
	for _, line := range []string{"first run\n", "second run\n"} {
		w, err := logdriver.NewWriter(
			t.Context(),
			string(logdriver.DriverK8sFile),
			"",
			map[string]string{logdriver.LogOptPath: logPath},
		)
		assert.NilError(t, err)
		assert.NilError(t, w.WriteLogLine(logdriver.LogLine{
			Time:   time.Now(),
			Stream: logdriver.StreamStdout,
			Line:   []byte(line),
		}))
		assert.NilError(t, w.Close())
	}

	var stdout, stderr bytes.Buffer
	opts := muxOpTestOptions(t, svc)
	opts.Stdout = &stdout
	opts.Stderr = &stderr
	replayMuxOpLog(t.Context(), opts, logPath)

	assert.Equal(t, stdout.String(), "first run\nsecond run\n")
	assert.Equal(t, stderr.String(), "")
}

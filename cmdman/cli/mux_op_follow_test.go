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
	"github.com/ngicks/cmdman/cmdman/eventlog"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/logdriver/k8sfile"
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

// appendMuxOpEvent writes one event to the service's event log, the way the
// worker's monitor does.
func appendMuxOpEvent(t *testing.T, cfg cmdman.CmdmanConfig, e model.Event) {
	t.Helper()

	path, err := cfg.EventLogPath()
	assert.NilError(t, err)
	w, err := eventlog.NewWriter(path)
	assert.NilError(t, err)
	assert.NilError(t, w.Append(e))
}

// What the follower is told about the worker's exit comes off the event log,
// and the entry it is looking for cannot be selected by when it was written.
func TestWaitMuxOpExit(t *testing.T) {
	// A host clock can step backwards, and a worker that exits after it has
	// done so stamps its exit earlier than the moment the follower subscribed.
	// The operation succeeded all the same, so that exit has to be delivered
	// rather than passed over as history from before the subscription.
	t.Run("delivers an exit stamped before the subscription", func(t *testing.T) {
		cfg := frameSvcConfig(t)
		svc := cmdman.NewService(cfg)
		defer svc.Close()

		const id = "0123456789ab"
		appendMuxOpEvent(t, cfg, model.Event{
			Time:     time.Now().Add(-time.Hour),
			Type:     model.EventTypeExited,
			ID:       id,
			ExitCode: new(0),
		})

		sub, err := subscribeMuxOpExit(t.Context(), svc, id)
		assert.NilError(t, err)
		defer func() { assert.NilError(t, sub.Close()) }()

		assert.NilError(t, waitMuxOpExit(t.Context(), svc, sub, id))
	})

	t.Run("reports the status the worker exited with", func(t *testing.T) {
		cfg := frameSvcConfig(t)
		svc := cmdman.NewService(cfg)
		defer svc.Close()

		const id = "ba9876543210"
		appendMuxOpEvent(t, cfg, model.Event{
			Time:     time.Now(),
			Type:     model.EventTypeExited,
			ID:       id,
			ExitCode: new(2),
		})

		sub, err := subscribeMuxOpExit(t.Context(), svc, id)
		assert.NilError(t, err)
		defer func() { assert.NilError(t, sub.Close()) }()

		err = waitMuxOpExit(t.Context(), svc, sub, id)
		exitErr, ok := errors.AsType[*ExitCodeError](err)
		assert.Assert(t, ok, "got %v", err)
		assert.Equal(t, exitErr.Code, 2)
	})

	// A worker can also fail without ever running: the monitor reports that as a
	// failure rather than as an exit, and there is no status to carry back — only
	// what it says, when it says anything.
	failures := []struct {
		name  string
		id    string
		error string
		want  string
	}{
		{
			name:  "passes on what the worker failed with",
			id:    "aaaa11112222",
			error: "exec: \"cmdman\": file does not exist",
			want:  "mux op worker failed: exec: \"cmdman\": file does not exist",
		},
		{
			name: "reports a failure that said nothing",
			id:   "bbbb33334444",
			want: "mux op worker failed",
		},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			cfg := frameSvcConfig(t)
			svc := cmdman.NewService(cfg)
			defer svc.Close()

			appendMuxOpEvent(t, cfg, model.Event{
				Time:  time.Now(),
				Type:  model.EventTypeFailed,
				ID:    tt.id,
				Error: tt.error,
			})

			sub, err := subscribeMuxOpExit(t.Context(), svc, tt.id)
			assert.NilError(t, err)
			defer func() { assert.NilError(t, sub.Close()) }()

			err = waitMuxOpExit(t.Context(), svc, sub, tt.id)
			assert.Error(t, err, tt.want)
		})
	}

	// A worker that was killed outright never says anything at all. Its record
	// going away is the only sign, and waiting on the event alone would then wait
	// for good — so the wait ends, once the event has had its grace period to
	// turn up after the record went.
	t.Run("gives up on a worker that died without reporting an exit", func(t *testing.T) {
		cfg := frameSvcConfig(t)
		svc := cmdman.NewService(cfg)
		defer svc.Close()

		// Nothing is ever registered under this id and nothing is ever appended
		// for it: the record is already gone, as it is for a worker killed
		// between its last output and its exit.
		const id = "cccc55556666"
		sub, err := subscribeMuxOpExit(t.Context(), svc, id)
		assert.NilError(t, err)
		defer func() { assert.NilError(t, sub.Close()) }()

		err = waitMuxOpExitWithin(
			t.Context(), svc, sub, id, 5*time.Millisecond, 10*time.Millisecond,
		)
		assert.Error(t, err, "mux op worker died without reporting an exit")
	})
}

// writeMuxOpRun appends one run's line to the log at logPath, through a writer
// of its own: an identity can be operated on again, and the run after adds to
// what the run before left rather than erasing it, exactly as two workers'
// monitors do over one path.
func writeMuxOpRun(t *testing.T, logPath, line string) {
	t.Helper()
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

// A worker that finishes before following starts takes its record with it, and
// the follow plumbing reads that record. Replaying the log file is what is left,
// and it has to reach the same place the live stream would have — this run's
// output, and only this run's.
func TestReplayMuxOpLog(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	defer svc.Close()

	replay := func(t *testing.T, logPath string, from k8sfile.Offset) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		opts := muxOpTestOptions(t, svc)
		opts.Stdout = &stdout
		opts.Stderr = &stderr
		replayMuxOpLog(t.Context(), opts, logPath, from)
		assert.Equal(t, stderr.String(), "")
		return stdout.String()
	}

	t.Run("prints the whole file when no run has been marked off", func(t *testing.T) {
		logPath, err := muxOpLogPath(cfg, "replay-whole")
		assert.NilError(t, err)
		writeMuxOpRun(t, logPath, "first run\n")
		writeMuxOpRun(t, logPath, "second run\n")

		assert.Equal(t, replay(t, logPath, k8sfile.Offset{}), "first run\nsecond run\n")
	})

	// What the runs before this one printed is still in the file, and printing it
	// again would put a failed run's error under the run that succeeded.
	t.Run("prints this run and not the runs before it", func(t *testing.T) {
		logPath, err := muxOpLogPath(cfg, "replay-boundary")
		assert.NilError(t, err)
		writeMuxOpRun(t, logPath, "error: the run before this one\n")

		from := muxOpLogEnd(t.Context(), logPath)
		writeMuxOpRun(t, logPath, "this run\n")

		assert.Equal(t, replay(t, logPath, from), "this run\n")
	})

	// A log that reached its size limit is replaced rather than kept, so a
	// boundary taken before that points past the end of the file that is there
	// now. What it marked off is gone either way; what is left is this run's.
	t.Run("prints what is left when the log was rotated under it", func(t *testing.T) {
		logPath, err := muxOpLogPath(cfg, "replay-rotated")
		assert.NilError(t, err)
		writeMuxOpRun(t, logPath, "the run before this one\n")
		writeMuxOpRun(t, logPath, "and the one before that\n")

		from := muxOpLogEnd(t.Context(), logPath)
		assert.NilError(t, os.Remove(logPath))
		writeMuxOpRun(t, logPath, "this run\n")

		assert.Equal(t, replay(t, logPath, from), "this run\n")
	})
}

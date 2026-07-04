package cmdman

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/internal/flock"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
	"gotest.tools/v3/assert"
)

func TestMonitorRunAndExit(t *testing.T) {
	dir := t.TempDir()
	appCfg := CmdmanConfig{
		DataDir:            dir,
		RuntimeDir:         dir,
		DefaultWorkingDir:  dir,
		DefaultEnvironment: testEnv(),
	}
	appCfg, err := appCfg.WithDefaults()
	assert.NilError(t, err)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-1"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "echo hello from monitor"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "test-echo", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run monitor synchronously (in this test process, not detached).
	err = RunMonitor(ctx, id, appCfg, logger)
	assert.NilError(t, err)

	// Verify final state.
	state, exitCode, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeExited)
	assert.Assert(t, exitCode != nil)
	assert.Equal(t, *exitCode, 0)

	// Verify exit history.
	history, err := st.GetExitHistory(id)
	assert.NilError(t, err)
	assert.Assert(t, len(history) > 0)
	assert.Equal(t, history[0].ExitCode, 0)
}

func TestMonitorNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	appCfg := CmdmanConfig{
		DataDir:            dir,
		RuntimeDir:         dir,
		DefaultWorkingDir:  dir,
		DefaultEnvironment: testEnv(),
	}
	appCfg, err := appCfg.WithDefaults()
	assert.NilError(t, err)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-2"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "exit 42"},
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = RunMonitor(ctx, id, appCfg, logger)
	assert.NilError(t, err)

	state, exitCode, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeExited)
	assert.Assert(t, exitCode != nil)
	assert.Equal(t, *exitCode, 42)
}

func TestMonitorAutoRemove(t *testing.T) {
	dir := t.TempDir()
	appCfg := CmdmanConfig{
		DataDir:            dir,
		RuntimeDir:         dir,
		DefaultWorkingDir:  dir,
		DefaultEnvironment: testEnv(),
	}
	appCfg, err := appCfg.WithDefaults()
	assert.NilError(t, err)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-3"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "true"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		Annotations:     map[string]string{store.AnnotationAutoRemove: "true"},
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = RunMonitor(ctx, id, appCfg, logger)
	assert.NilError(t, err)

	// Command should be auto-removed.
	_, resolveErr := st.ResolveID(id)
	assert.Assert(t, resolveErr != nil, "command should be removed")

	// Command dir should be removed.
	_, err = os.Stat(commandDir)
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "command dir should be removed")
}

func TestMonitorGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	appCfg := CmdmanConfig{
		DataDir:            dir,
		RuntimeDir:         dir,
		DefaultWorkingDir:  dir,
		DefaultEnvironment: testEnv(),
	}
	appCfg, err := appCfg.WithDefaults()
	assert.NilError(t, err)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-4"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "sleep 60"},
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Cancel after a short delay to simulate SIGTERM.
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	err = RunMonitor(ctx, id, appCfg, logger)
	// Should exit with an error from the killed process.
	// The monitor should handle this gracefully.
	assert.NilError(t, err)

	state, _, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeExited)
}

func TestStaleEntryCleanup(t *testing.T) {
	st := testStore(t)

	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: store.DefaultScrollbackBytes,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      "/tmp/cmd/stale-1",
	}
	assert.NilError(t, st.InsertCommandConfig("stale-1", "", cfg))
	// Set a PID that's definitely not alive (PID 1 is init, but use a very high PID).
	stateJSON := &model.CommandState{MonitorPID: 99999999}
	assert.NilError(t, st.InsertCommandState("stale-1", model.EventTypeRunning, stateJSON))

	cfgForCleanup, err := (CmdmanConfig{
		DataDir:            t.TempDir(),
		RuntimeDir:         t.TempDir(),
		DefaultWorkingDir:  "/tmp",
		DefaultEnvironment: testEnv(),
	}).WithDefaults()
	assert.NilError(t, err)
	assert.NilError(t, CleanStaleEntries(t.Context(), st, cfgForCleanup))

	state, _, _, err := st.GetCommandState("stale-1")
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeFailed)
}

func TestIsStaleMonitor(t *testing.T) {
	cfg, err := (CmdmanConfig{
		DataDir:            t.TempDir(),
		RuntimeDir:         t.TempDir(),
		DefaultWorkingDir:  "/tmp",
		DefaultEnvironment: testEnv(),
	}).WithDefaults()
	assert.NilError(t, err)

	const id = "probe-1"

	// No PID file: the monitor never started or already exited -> stale.
	stale, err := isStaleMonitor(cfg, id)
	assert.NilError(t, err)
	assert.Assert(t, stale)

	// Simulate a live monitor holding the flock on its PID file.
	pidPath, err := cfg.MonitorPIDPath(id)
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(filepath.Dir(pidPath), 0o700))
	f, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0o644)
	assert.NilError(t, err)
	defer f.Close()
	acquired, err := flock.TryLockExclusive(f)
	assert.NilError(t, err)
	assert.Assert(t, acquired)

	// A held lock reads as alive (not stale), even though a stale PID would
	// have passed the old Signal(0) check.
	stale, err = isStaleMonitor(cfg, id)
	assert.NilError(t, err)
	assert.Assert(t, !stale)

	// Releasing the lock (monitor crashed without removing its PID file)
	// reads as stale again.
	assert.NilError(t, flock.Unlock(f))
	stale, err = isStaleMonitor(cfg, id)
	assert.NilError(t, err)
	assert.Assert(t, stale)

	// An unexpected open failure (here a directory where the PID file should
	// be, which fails with something other than fs.ErrNotExist) is returned
	// to the caller, not silently classified as stale.
	const errID = "probe-err"
	errPidPath, err := cfg.MonitorPIDPath(errID)
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(errPidPath, 0o700))
	_, err = isStaleMonitor(cfg, errID)
	assert.Assert(t, err != nil)
	assert.Assert(t, !errors.Is(err, fs.ErrNotExist))
}

func TestCleanStaleEntrySkipsProbeError(t *testing.T) {
	st := testStore(t)

	cfg, err := (CmdmanConfig{
		DataDir:            t.TempDir(),
		RuntimeDir:         t.TempDir(),
		DefaultWorkingDir:  "/tmp",
		DefaultEnvironment: testEnv(),
	}).WithDefaults()
	assert.NilError(t, err)

	const id = "probe-skip"
	cmdCfg := &model.CommandConfig{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: store.DefaultScrollbackBytes,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      "/tmp/cmd/" + id,
	}
	assert.NilError(t, st.InsertCommandConfig(id, "", cmdCfg))
	stateJSON := &model.CommandState{MonitorPID: os.Getpid()}
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeRunning, stateJSON))

	// Force the liveness probe to fail with an unexpected error by putting a
	// directory where the PID file is expected.
	pidPath, err := cfg.MonitorPIDPath(id)
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(pidPath, 0o700))

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx := contextkey.WithSlogLogger(context.Background(), logger)

	// The probe error is logged and the entry is skipped, not aborted...
	assert.NilError(t, cleanStaleEntry(ctx, st, cfg, id, model.EventTypeRunning, stateJSON, cmdCfg))
	assert.Assert(t, strings.Contains(logBuf.String(), "probe monitor liveness failed"))

	// ...and the entry is left running rather than being marked failed.
	state, _, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeRunning)
}

func TestMonitorSubscribeCapturesOffsetAndLiveRecordsUnderLock(t *testing.T) {
	m := &Monitor{
		ring:              newRingBuffer(4096),
		outputBridge:      newBroadcaster[logdriver.LogLine](),
		stateChangeBridge: newBroadcaster[monitorStateChange](),
		logWriter:         testOffsetWriter{offset: "before"},
		terminalState:     newTerminalPaneState(),
	}

	sub := m.subscribeOutput(false)
	defer sub.Unsub()
	assert.Equal(t, sub.Offset, "before")

	m.outputMu.Lock()
	m.logWriter = testOffsetWriter{offset: "after"}
	m.outputBridge.Send(logdriver.LogLine{
		Stream: logdriver.StreamStdout,
		Line:   []byte("live\n"),
	})
	m.outputMu.Unlock()

	select {
	case line := <-sub.Records:
		assert.Equal(t, string(line.Line), "live\n")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live record")
	}

	sub2 := m.subscribeOutput(false)
	defer sub2.Unsub()
	assert.Equal(t, sub2.Offset, "after")
	select {
	case line := <-sub2.Records:
		t.Fatalf("second subscriber unexpectedly received old line %q", line.Line)
	default:
	}
}

func TestMonitorSubscribeWithScrollbackIncludesTerminalModeReplay(t *testing.T) {
	m := &Monitor{
		ring:              newRingBuffer(16),
		outputBridge:      newBroadcaster[logdriver.LogLine](),
		stateChangeBridge: newBroadcaster[monitorStateChange](),
		terminalState:     newTerminalPaneState(),
		// A non-TTY command: scrollback is the raw ring buffer (no screen mirror).
		cfg: &model.CommandConfig{},
	}
	m.terminalState.Observe([]byte("\x1b[?1000;1006;2004h"))
	_, _ = m.ring.Write([]byte("tail-only\n"))

	sub := m.subscribeOutput(true)
	defer sub.Unsub()

	assert.Equal(t, string(sub.Scrollback), "tail-only\n")
	assert.Equal(t, string(sub.TerminalMode), "\x1b[?1000;1006;2004h")
}

func TestMonitorStateChangeBroadcastsTerminalStateAndCloses(t *testing.T) {
	st := testStore(t)
	id := "state-change"
	assert.NilError(t, st.InsertCommandConfig(id, "", &model.CommandConfig{
		Argv:       []string{"/bin/true"},
		Dir:        t.TempDir(),
		Env:        testEnv(),
		CommandDir: t.TempDir(),
	}))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{}))

	m := &Monitor{
		ID:                id,
		store:             st,
		stateJSON:         &model.CommandState{},
		stateChangeBridge: newBroadcaster[monitorStateChange](),
	}
	ch, unsub := m.subscribeStateChange()
	defer unsub()

	m.setExited(7)

	select {
	case state, ok := <-ch:
		assert.Assert(t, ok)
		assert.Equal(t, state.State, model.EventTypeExited)
		assert.Equal(t, state.ExitCode, 7)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for exited state change")
	}
	select {
	case _, ok := <-ch:
		assert.Assert(t, !ok)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state change close")
	}

	lateCh, lateUnsub := m.subscribeStateChange()
	defer lateUnsub()
	select {
	case _, ok := <-lateCh:
		assert.Assert(t, !ok)
	case <-time.After(time.Second):
		t.Fatal("late subscriber blocked on closed state bridge")
	}
}

type testOffsetWriter struct {
	offset any
}

func (w testOffsetWriter) WriteLogLine(logdriver.LogLine) error { return nil }
func (w testOffsetWriter) Close() error                         { return nil }
func (w testOffsetWriter) CurrentOffset() any                   { return w.offset }

package cmdman

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
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
	assert.NilError(t, st.InsertCommandState("stale-1", model.EventTypeStarted, stateJSON))

	cfgForCleanup, err := (CmdmanConfig{
		DataDir:            t.TempDir(),
		RuntimeDir:         t.TempDir(),
		DefaultWorkingDir:  "/tmp",
		DefaultEnvironment: testEnv(),
	}).WithDefaults()
	assert.NilError(t, err)
	assert.NilError(t, CleanStaleEntries(st, cfgForCleanup))

	state, _, _, err := st.GetCommandState("stale-1")
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeFailed)
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
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeStarted, &model.CommandState{}))

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

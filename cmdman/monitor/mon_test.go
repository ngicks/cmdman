package monitor

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

	pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/cmdman/internal/flock"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"gotest.tools/v3/assert"
)

func TestMonitorRunAndExit(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
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
	appCfg := testConfig(t, dir)
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
	appCfg := testConfig(t, dir)
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
	appCfg := testConfig(t, dir)
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

// A run whose config file disappears is a terminal path like any other: it must
// close what clients park on. A stream left open would hold the GracefulStop
// that the supervisor waits on, and the monitor would never exit.
func TestMonitorShutsDownWhenConfigReadFails(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-5"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv: []string{"/bin/sh", "-c", "sleep 1"},
		Dir:  dir,
		Env:  testEnv(),
		// The command must come back for the loop to re-read the config it no
		// longer has.
		RestartPolicy:   model.RestartPolicyAlways,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Generous, so that what the test proves is the monitor shutting itself
	// down rather than the context doing it for the monitor.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- RunMonitor(ctx, id, appCfg, logger) }()

	waitUntil(t, 10*time.Second, func() bool {
		state, _, _, err := st.GetCommandState(id)
		return err == nil && state == model.EventTypeRunning
	}, "monitor never reached running")

	sockPath, err := appCfg.MonitorSocketPath(id)
	assert.NilError(t, err)
	conn, err := grpc.NewClient(
		"unix://"+sockPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	assert.NilError(t, err)
	defer conn.Close()

	watch, err := pb.NewCommandMonitorServiceClient(conn).
		WatchRuntimeState(ctx, &pb.WatchRuntimeStateRequest{})
	assert.NilError(t, err)
	// The unconditional first snapshot proves the handler is registered and
	// parked - which is exactly what GracefulStop waits for.
	_, err = watch.Recv()
	assert.NilError(t, err)

	assert.NilError(t, os.Remove(store.CommandConfigPath(commandDir)))

	select {
	case err := <-done:
		assert.ErrorContains(t, err, "read config")
	case <-time.After(20 * time.Second):
		t.Fatal("monitor did not shut down after its config file disappeared")
	}

	state, _, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeFailed)
}

// D13: runtime state dies with the run, and it dies when the run does - not
// when a later run gets far enough to reset it, which is never if that run's
// setup fails.
func TestMonitorRunEndClearsRuntimeState(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-6"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", `printf '\033]2;from-run\007'; sleep 1`},
		Dir:             dir,
		Env:             testEnv(),
		Tty:             true,
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

	changed, unsub := m.runtimeState.subscribeChange()
	defer unsub()

	// The run's error comes back to the test goroutine: a failed assertion calls
	// FailNow, which only the test goroutine may do.
	runErr := make(chan error, 1)
	go func() {
		_, err := m.runOnce(t.Context())
		runErr <- err
	}()

	// Observing the title first keeps the assertion below from passing on a run
	// that never latched anything.
	waitUntil(t, 10*time.Second, func() bool {
		return m.runtimeState.snapshot().Title == "from-run"
	}, "the run never latched its title")
	for woke(changed) {
	}

	select {
	case err := <-runErr:
		assert.NilError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("the run never finished")
	}

	assert.Equal(t, m.runtimeState.snapshot().Title, "")
	// The clearing wakes watchers, so a WatchRuntimeState stream sees the run's
	// title go away instead of holding it until the next run.
	assert.Assert(t, woke(changed))
}

// A command that never emits OSC 7 - here a pipe-wired one, which has no
// emulator to emit it through at all - still reports the directory it was
// started in, from the moment the run begins.
func TestMonitorRunSeedsCwdFromConfiguredDir(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-7"
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

	// The run's error comes back to the test goroutine: a failed assertion calls
	// FailNow, which only the test goroutine may do.
	runErr := make(chan error, 1)
	go func() {
		_, err := m.runOnce(t.Context())
		runErr <- err
	}()

	waitUntil(t, 10*time.Second, func() bool {
		return m.runtimeState.snapshot().CwdSet
	}, "the run never seeded its cwd")
	assert.Equal(t, m.runtimeState.snapshot().Cwd, cwdURL(dir))

	select {
	case err := <-runErr:
		assert.NilError(t, err)
	case <-time.After(20 * time.Second):
		t.Fatal("the run never finished")
	}

	// The seed is per-run state like everything else the run latched: it dies
	// with the run rather than describing a process that is gone.
	assert.Equal(t, m.runtimeState.snapshot().CwdSet, false)
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

	cfgForCleanup := testConfig(t, t.TempDir())
	assert.NilError(t, CleanStaleEntries(t.Context(), st, cfgForCleanup))

	state, _, _, err := st.GetCommandState("stale-1")
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeFailed)
}

func TestIsStaleMonitor(t *testing.T) {
	cfg := testConfig(t, t.TempDir())

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

	cfg := testConfig(t, t.TempDir())

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

func TestMonitorSubscribeWithScrollbackIncludesTerminalStateReplay(t *testing.T) {
	runtimeState := newCommandRuntimeState()
	m := &Monitor{
		ring:              newRingBuffer(16),
		outputBridge:      newBroadcaster[logdriver.LogLine](),
		stateChangeBridge: newBroadcaster[monitorStateChange](),
		terminalState:     newTerminalPaneState(),
		runtimeState:      runtimeState,
		// With no screen mirror, a TTY subscription falls back to raw ring bytes.
		cfg: &model.CommandConfig{Tty: true},
	}
	m.terminalState.Observe([]byte("\x1b[?1000;1006;2004h"))
	runtimeState.latchTitle("build")
	_, _ = m.ring.Write([]byte("tail-only\n"))

	sub := m.subscribeOutput(true)
	defer sub.Unsub()

	assert.Equal(t, string(sub.Scrollback), "tail-only\n")
	assert.Equal(
		t,
		string(sub.TerminalState),
		"\x1b[?1000;1006;2004h\x1b]2;build\x07",
	)
}

// The payload carries a literal space and an already-percent-encoded octet, so
// any round-trip through a URL type on the replay path would rewrite it and
// fail the comparison.
const testCwdPayload = "file://localhost/tmp/my dir/%20odd"

func TestMonitorSubscribeReplaysLatchedCwdAfterTitle(t *testing.T) {
	runtimeState := newCommandRuntimeState()
	m := &Monitor{
		ring:              newRingBuffer(16),
		outputBridge:      newBroadcaster[logdriver.LogLine](),
		stateChangeBridge: newBroadcaster[monitorStateChange](),
		terminalState:     newTerminalPaneState(),
		runtimeState:      runtimeState,
		cfg:               &model.CommandConfig{Tty: true},
	}
	m.terminalState.Observe([]byte("\x1b[?1000h"))
	runtimeState.latchTitle("build")
	runtimeState.latchCwd(testCwdPayload)

	sub := m.subscribeOutput(true)
	defer sub.Unsub()

	assert.Equal(
		t,
		string(sub.TerminalState),
		"\x1b[?1000h\x1b]2;build\x07\x1b]7;"+testCwdPayload+"\x07",
	)
}

func TestMonitorSubscribeReplaysLatchedCwdWithoutTitle(t *testing.T) {
	runtimeState := newCommandRuntimeState()
	runtimeState.latchCwd(testCwdPayload)
	m := &Monitor{
		ring:              newRingBuffer(16),
		outputBridge:      newBroadcaster[logdriver.LogLine](),
		stateChangeBridge: newBroadcaster[monitorStateChange](),
		terminalState:     newTerminalPaneState(),
		runtimeState:      runtimeState,
		cfg:               &model.CommandConfig{Tty: true},
	}

	sub := m.subscribeOutput(true)
	defer sub.Unsub()

	assert.Equal(t, string(sub.TerminalState), "\x1b]7;"+testCwdPayload+"\x07")
}

func TestMonitorSubscribeReplaysExplicitlyClearedTitle(t *testing.T) {
	runtimeState := newCommandRuntimeState()
	runtimeState.latchTitle("")
	m := &Monitor{
		ring:              newRingBuffer(16),
		outputBridge:      newBroadcaster[logdriver.LogLine](),
		stateChangeBridge: newBroadcaster[monitorStateChange](),
		terminalState:     newTerminalPaneState(),
		runtimeState:      runtimeState,
		cfg:               &model.CommandConfig{Tty: true},
	}

	sub := m.subscribeOutput(true)
	defer sub.Unsub()

	assert.Equal(t, string(sub.TerminalState), "\x1b]2;\x07")
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

func TestMonitorServerCaptureScreen(t *testing.T) {
	const cols, rows = 20, 3

	for _, tc := range []struct {
		name     string
		withTty  bool
		feed     string
		req      *pb.CaptureScreenRequest
		want     string
		wantCode codes.Code
	}{
		{
			name:    "visible screen",
			withTty: true,
			feed:    "a\r\nb\r\n",
			req:     &pb.CaptureScreenRequest{},
			want:    "a\nb\n\n",
		},
		{
			// Proves the range fields reach captureOptions: without has_start the
			// negative start_line would be ignored and history stay out.
			name:    "history range fields map through",
			withTty: true,
			feed:    "a\r\nb\r\nc\r\nd\r\n",
			req: &pb.CaptureScreenRequest{
				HasStart:  true,
				StartLine: -1,
				HasEnd:    true,
				EndLine:   0,
			},
			// Four lines on a three-row screen scroll "a" and "b" into history,
			// so -1..0 is the newest history line plus the topmost visible one.
			want: "b\nc\n",
		},
		{
			name:     "no alternate screen",
			withTty:  true,
			feed:     "a\r\n",
			req:      &pb.CaptureScreenRequest{AltScreen: true},
			wantCode: codes.FailedPrecondition,
		},
		{
			name:    "quiet alternate screen captures nothing",
			withTty: true,
			feed:    "a\r\n",
			req:     &pb.CaptureScreenRequest{AltScreen: true, Quiet: true},
			want:    "",
		},
		{
			name:     "no screen mirror",
			req:      &pb.CaptureScreenRequest{},
			wantCode: codes.FailedPrecondition,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Monitor{cfg: &model.CommandConfig{Tty: tc.withTty}}
			if tc.withTty {
				m.screen = newScreenTracker(cols, rows, newCommandRuntimeState())
				t.Cleanup(m.screen.close)
				m.screen.feed([]byte(tc.feed))
			}

			s := &monitorServer{monitor: m}
			resp, err := s.CaptureScreen(context.Background(), tc.req)
			if tc.wantCode != codes.OK {
				assert.Equal(t, status.Code(err), tc.wantCode)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, string(resp.Content), tc.want)
		})
	}
}

type testOffsetWriter struct {
	offset any
}

func (w testOffsetWriter) WriteLogLine(logdriver.LogLine) error { return nil }
func (w testOffsetWriter) Close() error                         { return nil }
func (w testOffsetWriter) CurrentOffset() any                   { return w.offset }

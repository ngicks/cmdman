package cmdman

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ngicks/crabswarm/pkg/cmdman/store"
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

	st, err := store.OpenStore(dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-1"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &store.CommandConfigJSON{
		Argv:            []string{"/bin/sh", "-c", "echo hello from monitor"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   store.RestartPolicyNo,
		ScrollbackBytes: 4096,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "test-echo", cfg))
	assert.NilError(t, cfg.Write())
	assert.NilError(t, st.InsertCommandState(id, store.StateCreated, &store.CommandStateJSON{}))

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run monitor synchronously (in this test process, not detached).
	err = RunMonitor(ctx, id, appCfg, logger)
	assert.NilError(t, err)

	// Verify final state.
	state, exitCode, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, store.StateExited)
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

	st, err := store.OpenStore(dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-2"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &store.CommandConfigJSON{
		Argv:            []string{"/bin/sh", "-c", "exit 42"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   store.RestartPolicyNo,
		ScrollbackBytes: 4096,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, cfg.Write())
	assert.NilError(t, st.InsertCommandState(id, store.StateCreated, &store.CommandStateJSON{}))

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = RunMonitor(ctx, id, appCfg, logger)
	assert.NilError(t, err)

	state, exitCode, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, store.StateExited)
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

	st, err := store.OpenStore(dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-3"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &store.CommandConfigJSON{
		Argv:            []string{"/bin/sh", "-c", "true"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   store.RestartPolicyNo,
		ScrollbackBytes: 4096,
		Annotations:     map[string]string{store.AnnotationAutoRemove: "true"},
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, cfg.Write())
	assert.NilError(t, st.InsertCommandState(id, store.StateCreated, &store.CommandStateJSON{}))

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
	assert.Assert(t, os.IsNotExist(err), "command dir should be removed")
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

	st, err := store.OpenStore(dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-4"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &store.CommandConfigJSON{
		Argv:            []string{"/bin/sh", "-c", "sleep 60"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   store.RestartPolicyNo,
		ScrollbackBytes: 4096,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, cfg.Write())
	assert.NilError(t, st.InsertCommandState(id, store.StateCreated, &store.CommandStateJSON{}))

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
	assert.Equal(t, state, store.StateExited)
}

func TestStaleEntryCleanup(t *testing.T) {
	st := testStore(t)

	cfg := &store.CommandConfigJSON{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   store.RestartPolicyNo,
		ScrollbackBytes: store.DefaultScrollbackBytes,
		CommandDir:      "/tmp/cmd/stale-1",
	}
	assert.NilError(t, st.InsertCommandConfig("stale-1", "", cfg))
	// Set a PID that's definitely not alive (PID 1 is init, but use a very high PID).
	stateJSON := &store.CommandStateJSON{MonitorPID: 99999999}
	assert.NilError(t, st.InsertCommandState("stale-1", store.StateRunning, stateJSON))

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
	assert.Equal(t, state, store.StateFailed)
}

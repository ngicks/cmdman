package cmdman

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"gotest.tools/v3/assert"
)

func TestRestartPolicyOnFailure(t *testing.T) {
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

	id := "test-restart-1"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)

	// Write a script that fails twice then succeeds.
	scriptPath := filepath.Join(dir, "countdown.sh")
	os.WriteFile(scriptPath, []byte(`#!/bin/sh
COUNTER_FILE="$1"
count=$(cat "$COUNTER_FILE" 2>/dev/null || echo 0)
count=$((count + 1))
echo "$count" > "$COUNTER_FILE"
if [ "$count" -lt 3 ]; then
  exit 1
fi
exit 0
`), 0o755)

	counterFile := filepath.Join(dir, "counter")

	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", scriptPath, counterFile},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyOnFailure,
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

	// Should have exited successfully after 3 runs.
	state, exitCode, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeExited)
	assert.Assert(t, exitCode != nil)
	assert.Equal(t, *exitCode, 0)

	// Should have 3 exit history entries.
	history, err := st.GetExitHistory(id)
	assert.NilError(t, err)
	assert.Equal(t, len(history), 3)
}

func TestRestartPolicyAlways(t *testing.T) {
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

	id := "test-restart-always"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "true"},
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

	// Cancel after a short time to stop the always-restart loop.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = RunMonitor(ctx, id, appCfg, logger)
	assert.NilError(t, err)

	// Should have multiple exit history entries.
	history, err := st.GetExitHistory(id)
	assert.NilError(t, err)
	assert.Assert(t, len(history) >= 2, "expected at least 2 restarts, got %d", len(history))
}

func TestRestartPolicyOnFailureMaxRetries(t *testing.T) {
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

	id := "test-restart-maxretries"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)

	// A command that always fails; with MaxRetries=2 the monitor runs it once
	// and retries twice (3 runs total) before giving up.
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "exit 1"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyOnFailure,
		MaxRetries:      2,
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

	// Gave up after the retry budget; final state is exited with the failure code.
	state, exitCode, _, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeExited)
	assert.Assert(t, exitCode != nil)
	assert.Equal(t, *exitCode, 1)

	// 1 initial run + 2 retries = 3 exit history entries.
	history, err := st.GetExitHistory(id)
	assert.NilError(t, err)
	assert.Equal(t, len(history), 3)
}

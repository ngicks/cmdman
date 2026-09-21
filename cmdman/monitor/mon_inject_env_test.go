package monitor

import (
	"log/slog"
	"os"
	"slices"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// A command whose config turns injection off keeps the CMDMAN_* context an
// outer cmdman left in its environment, while the hooks attached to it still
// describe the command this monitor supervises.
func TestMonitorInjectEnvDisabledExemptsHooks(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-inject-env-off"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	outer := config.ENV_CMDMAN_CMD_ID + "=outer-id"
	own := config.ENV_CMDMAN_CMD_ID + "=" + id
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "exit 0"},
		Dir:             dir,
		Env:             append(testEnv(), outer),
		InjectEnv:       false,
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

	cmd, err := m.wireUpCmd(t.Context())
	assert.NilError(t, err)
	assert.Assert(t, slices.Contains(cmd.Env, outer))
	assert.Assert(t, !slices.Contains(cmd.Env, own))

	code, err := m.runOnce(t.Context())
	assert.NilError(t, err)
	assert.Equal(t, code, 0)

	// The dispatcher holds the env a hook of the run that just ended would have
	// been given, so what the run configured is what a hook would have seen.
	_, _, hookEnv := m.hooks.resolve(model.HookEventBell)
	assert.Assert(t, slices.Contains(hookEnv, own))
	assert.Assert(t, !slices.Contains(hookEnv, outer))
}

// With injection on, the monitor replaces an inherited CMDMAN_CMD_ID with its
// own before the command starts.
func TestMonitorInjectEnvReplacesInheritedContext(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-inject-env-on"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	outer := config.ENV_CMDMAN_CMD_ID + "=outer-id"
	own := config.ENV_CMDMAN_CMD_ID + "=" + id
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "exit 0"},
		Dir:             dir,
		Env:             append(testEnv(), outer),
		InjectEnv:       true,
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

	cmd, err := m.wireUpCmd(t.Context())
	assert.NilError(t, err)
	assert.Assert(t, slices.Contains(cmd.Env, own))
	assert.Assert(t, !slices.Contains(cmd.Env, outer))
	assert.Assert(t, slices.Contains(cmd.Env, config.ENV_CMDMAN_CMD_DATA_DIR+"="+commandDir))
}

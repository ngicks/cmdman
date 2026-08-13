package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/internal/flock"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/cmdman/store"
	"gotest.tools/v3/assert"
)

// frameSvcConfig is a cmdman configuration rooted at a temp dir, so the service
// under test has a store and a runtime dir of its own.
func frameSvcConfig(t *testing.T) cmdman.CmdmanConfig {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.DefaultConfig()
	assert.NilError(t, err)
	cfg.DataDir = dir
	cfg.RuntimeDir = dir
	cfg.DefaultWorkingDir = dir
	assert.NilError(t, cfg.Validate())
	return cfg
}

// TestFrameSvc drives the adapter against a real cmdman service — the half the
// mux-side state-machine tests cannot reach, since the cmdman package imports
// that one. What it pins is what a fake cannot answer for: where a created
// command's fields land, and what the listing reports for a command whose
// monitor holds its pid lock.
func TestFrameSvc(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	defer svc.Close()
	adapter := NewFrameSvc(svc)
	ctx := t.Context()

	// The spec a managed frame entry becomes (D19/V7).
	hooks := model.HookSet{model.HookEventBell: {Action: model.HookActionBlock}}
	spec := mux.CommandSpec{
		Name:  "frame-dev-0",
		Argv:  []string{"/bin/sh", "-c", "sleep 60"},
		Tty:   true,
		Hooks: hooks,
	}

	id, err := adapter.CreateCommand(ctx, spec)
	assert.NilError(t, err)

	out, err := svc.Inspect(ctx, "frame-dev-0")
	assert.NilError(t, err)
	assert.Equal(t, out.ID, id)
	// The entry's hook override ends up on the supervised command's own config,
	// which is the layer that beats the config-level default_hooks (D17/V5) —
	// no frame-shaped layer of its own.
	assert.DeepEqual(t, out.Config.Hooks, hooks)
	assert.DeepEqual(t, out.Config.Argv, spec.Argv)
	assert.Equal(t, out.Config.Tty, true)

	// A record with no monitor behind it: listed, in a state the frame verbs
	// start again rather than replace.
	assert.DeepEqual(t, listCommands(t, adapter), []mux.CommandStatus{
		{ID: id, Name: "frame-dev-0", State: model.EventTypeCreated},
	})

	// V7's adopt half rests on this: a live command survives the stale sweep
	// the service's listing runs, so the frame verbs read running rather than
	// failed.
	markRunningMonitor(t, cfg, id)
	assert.DeepEqual(t, listCommands(t, adapter), []mux.CommandStatus{
		{ID: id, Name: "frame-dev-0", State: model.EventTypeRunning},
	})
}

func listCommands(t *testing.T, svc mux.FrameSvc) []mux.CommandStatus {
	t.Helper()
	commands, err := svc.ListCommands(t.Context())
	assert.NilError(t, err)
	return commands
}

// markRunningMonitor puts id in the running state and makes it survive the
// stale sweep the service's listing runs: liveness is the flock on the
// monitor's PID file, so the test holds that lock itself in place of a monitor
// process.
func markRunningMonitor(t *testing.T, cfg cmdman.CmdmanConfig, id string) {
	t.Helper()

	pidPath, err := cfg.MonitorPIDPath(id)
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(filepath.Dir(pidPath), 0o755))
	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_RDWR, 0o644)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	acquired, err := flock.TryLockExclusive(f)
	assert.NilError(t, err)
	assert.Assert(t, acquired, "the test must hold the pid lock a monitor would")

	dbPath, err := cfg.DBPath()
	assert.NilError(t, err)
	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer func() { assert.NilError(t, st.Close()) }()
	assert.NilError(t, st.UpdateCommandState(
		id, model.EventTypeRunning, nil, &model.CommandState{MonitorPID: os.Getpid()},
	))
}

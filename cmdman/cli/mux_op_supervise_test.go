package cli

import (
	"os"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// muxOpTestOptions is a supervised MuxOpOptions against a service rooted at a
// temp dir.
func muxOpTestOptions(t *testing.T, svc *cmdman.Service) MuxOpOptions {
	t.Helper()
	opts, err := MuxOpOptions{
		Svc:     svc,
		LogName: MuxOpLogName("", "dashboard"),
		Argv:    []string{"mux", "up", "dashboard.yaml"},
		Env:     []string{"PATH=/usr/bin"},
		Dir:     t.TempDir(),
	}.resolve()
	assert.NilError(t, err)
	return opts
}

// backdateCommand moves id's registration timestamp back by age, which is how a
// test asks for a record old enough to have outlived the grace a record gets
// before it has a monitor to be judged by.
func backdateCommand(t *testing.T, cfg cmdman.CmdmanConfig, id string, age time.Duration) {
	t.Helper()

	dbPath, err := cfg.DBPath()
	assert.NilError(t, err)
	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer func() { assert.NilError(t, st.Close()) }()

	stamp := time.Now().UTC().Add(-age).Format(time.RFC3339)
	_, err = st.DB().Exec(`UPDATE CommandConfig SET CreatedAt = ? WHERE ID = ?`, stamp, id)
	assert.NilError(t, err)
}

// Registering the worker under a name derived from the window is what keeps two
// operations off it, so what a name conflict means has to be told apart
// correctly: a leftover from a worker that never cleaned up must not lock the
// window out, and a live operation must not have its record pulled out from
// under it.
func TestCreateMuxOpCommand(t *testing.T) {
	t.Run("registers when the name is free", func(t *testing.T) {
		svc := cmdman.NewService(frameSvcConfig(t))
		defer svc.Close()

		req, err := muxOpCreateRequest(muxOpTestOptions(t, svc))
		assert.NilError(t, err)

		id, err := createMuxOpCommand(t.Context(), svc, req)
		assert.NilError(t, err)
		assert.Assert(t, id != "")

		entry, err := findCommandByName(t.Context(), svc, req.Name)
		assert.NilError(t, err)
		assert.Assert(t, entry != nil)
		assert.Equal(t, entry.ID, id)
	})

	t.Run("replaces a leftover record", func(t *testing.T) {
		cfg := frameSvcConfig(t)
		svc := cmdman.NewService(cfg)
		defer svc.Close()

		req, err := muxOpCreateRequest(muxOpTestOptions(t, svc))
		assert.NilError(t, err)

		stale, err := createMuxOpCommand(t.Context(), svc, req)
		assert.NilError(t, err)
		// A record that never left the created state is only a leftover once it
		// is too old to be a worker still spawning.
		backdateCommand(t, cfg, stale, 2*muxOpPreRunGrace)

		fresh, err := createMuxOpCommand(t.Context(), svc, req)
		assert.NilError(t, err)
		assert.Assert(t, fresh != stale, "the leftover was reused instead of replaced")

		entry, err := findCommandByName(t.Context(), svc, req.Name)
		assert.NilError(t, err)
		assert.Assert(t, entry != nil)
		assert.Equal(t, entry.ID, fresh)
	})

	// The window this covers is the one a second invocation races: the first has
	// registered its worker but no monitor of it is up yet, so nothing but the
	// record's age says the worker is alive.
	t.Run("refuses while an operation is still spawning", func(t *testing.T) {
		svc := cmdman.NewService(frameSvcConfig(t))
		defer svc.Close()

		req, err := muxOpCreateRequest(muxOpTestOptions(t, svc))
		assert.NilError(t, err)

		spawning, err := createMuxOpCommand(t.Context(), svc, req)
		assert.NilError(t, err)

		_, err = createMuxOpCommand(t.Context(), svc, req)
		assert.ErrorContains(t, err, "already running")

		entry, err := findCommandByName(t.Context(), svc, req.Name)
		assert.NilError(t, err)
		assert.Assert(t, entry != nil, "the spawning operation's record was removed")
		assert.Equal(t, entry.ID, spawning)
		assert.Equal(t, entry.State, model.EventTypeCreated)
	})

	t.Run("refuses while an operation is running", func(t *testing.T) {
		cfg := frameSvcConfig(t)
		svc := cmdman.NewService(cfg)
		defer svc.Close()

		req, err := muxOpCreateRequest(muxOpTestOptions(t, svc))
		assert.NilError(t, err)

		running, err := createMuxOpCommand(t.Context(), svc, req)
		assert.NilError(t, err)
		markRunningMonitor(t, cfg, running)

		_, err = createMuxOpCommand(t.Context(), svc, req)
		assert.ErrorContains(t, err, "already running")

		entry, err := findCommandByName(t.Context(), svc, req.Name)
		assert.NilError(t, err)
		assert.Assert(t, entry != nil, "the live operation's record was removed")
		assert.Equal(t, entry.ID, running)
		assert.Equal(t, entry.State, model.EventTypeRunning)
	})
}

// The worker is this binary re-run with what the user typed, and it has to land
// in the same store: it inherits the environment but not the flags, so the
// resolved dirs travel as flags of their own.
func TestMuxOpCreateRequest(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	defer svc.Close()

	opts := muxOpTestOptions(t, svc)
	req, err := muxOpCreateRequest(opts)
	assert.NilError(t, err)

	exe, err := os.Executable()
	assert.NilError(t, err)
	assert.DeepEqual(t, req.Argv, []string{
		exe,
		"--data-dir", cfg.DataDir,
		"--runtime-dir", cfg.RuntimeDir,
		"mux", "up", "dashboard.yaml",
	})

	assert.Equal(t, req.Name, muxOpCommandName(opts.LogName))
	assert.Equal(t, req.AutoRemove, true)
	assert.Equal(t, req.RestartPolicy, model.RestartPolicyNo)
	assert.Assert(t, req.ImportHostEnv != nil && !*req.ImportHostEnv)
	assert.DeepEqual(t, req.Env, []string{"PATH=/usr/bin", envMuxOp + "=1"})

	// The record is auto-removed, so the diagnostic log has to outlive the
	// command directory it would otherwise sit in.
	assert.Equal(t, req.LogOpts["path"], cfg.RuntimeDir+"/mux/"+opts.LogName+".log")
	assert.Equal(t, req.LogOpts["max-size"], "1MiB")
	assert.Equal(t, req.LogOpts["max-file"], "1")

	_, err = os.Stat(cfg.RuntimeDir + "/mux")
	assert.NilError(t, err, "the log directory is not created on demand")
}

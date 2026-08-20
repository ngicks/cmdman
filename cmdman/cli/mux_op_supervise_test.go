package cli

import (
	"os"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
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
		svc := cmdman.NewService(frameSvcConfig(t))
		defer svc.Close()

		req, err := muxOpCreateRequest(muxOpTestOptions(t, svc))
		assert.NilError(t, err)

		stale, err := createMuxOpCommand(t.Context(), svc, req)
		assert.NilError(t, err)

		fresh, err := createMuxOpCommand(t.Context(), svc, req)
		assert.NilError(t, err)
		assert.Assert(t, fresh != stale, "the leftover was reused instead of replaced")

		entry, err := findCommandByName(t.Context(), svc, req.Name)
		assert.NilError(t, err)
		assert.Assert(t, entry != nil)
		assert.Equal(t, entry.ID, fresh)
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

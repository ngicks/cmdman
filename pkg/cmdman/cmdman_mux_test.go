package cmdman

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// TestServiceMuxResolver verifies the standalone-mux leaf resolver returned by
// MuxResolver: scaleIndex 0 resolves the bare leaf name, and a positive
// scaleIndex resolves the suffixed "<leaf>-<N>" replica name. Both return the
// resolved command's store ID.
func TestServiceMuxResolver(t *testing.T) {
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

	// Two commands: a bare leaf "web" and its pinned replica "web-2".
	seed := func(id, name string) {
		cfg := &model.CommandConfig{Argv: []string{"true"}, Dir: dir, Env: testEnv()}
		assert.NilError(t, st.InsertCommandConfig(id, name, cfg))
		assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))
	}
	seed("id-web", "web")
	seed("id-web-2", "web-2")
	assert.NilError(t, st.Close())

	svc := NewService(appCfg)
	defer svc.Close()
	resolver := svc.MuxResolver()

	t.Run("plain branch resolves bare leaf name", func(t *testing.T) {
		id, err := resolver(t.Context(), "web", 0)
		assert.NilError(t, err)
		assert.Equal(t, id, "id-web")
	})

	t.Run("pinned branch resolves <leaf>-<N> suffix", func(t *testing.T) {
		id, err := resolver(t.Context(), "web", 2)
		assert.NilError(t, err)
		assert.Equal(t, id, "id-web-2")
	})
}

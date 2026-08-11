package monitor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/store"
	"gotest.tools/v3/assert"

	// Register the k8s-file log driver (model.DefaultLogDriver) for tests that
	// run the monitor loop directly via RunMonitor. In the cmdman binary the
	// driver is registered transitively through pkg/cmdman; the monitor
	// package's own test binary needs this blank import to make it available.
	_ "github.com/ngicks/cmdman/cmdman/logdriver/k8sfile"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}

func testEnv() []string {
	return append([]string(nil), os.Environ()...)
}

// testConfig is the built-in default configuration with every path rooted at
// dir, i.e. what config.Load would return for a run whose data and runtime dirs
// point into the test's own directory.
func testConfig(t *testing.T, dir string) config.Config {
	t.Helper()
	cfg, err := config.DefaultConfig()
	assert.NilError(t, err)
	cfg.DataDir = dir
	cfg.RuntimeDir = dir
	cfg.DefaultWorkingDir = dir
	assert.NilError(t, cfg.Validate())
	return cfg
}

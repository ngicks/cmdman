package cmdman

import (
	"os"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/config"
)

func testEnv() []string {
	return append([]string(nil), os.Environ()...)
}

// testConfig is the built-in default configuration with every path rooted at
// dir, i.e. what config.Load would return for a run whose data and runtime dirs
// point into the test's own directory.
func testConfig(t *testing.T, dir string) CmdmanConfig {
	t.Helper()
	cfg, err := config.DefaultConfig()
	assert.NilError(t, err)
	cfg.DataDir = dir
	cfg.RuntimeDir = dir
	cfg.DefaultWorkingDir = dir
	assert.NilError(t, cfg.Validate())
	return cfg
}

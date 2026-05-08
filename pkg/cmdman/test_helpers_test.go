package cmdman

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"gotest.tools/v3/assert"
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

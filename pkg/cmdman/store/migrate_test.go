package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store/migration"
	"gotest.tools/v3/assert"
)

// execRaw opens a bare connection to dbPath and runs the given statements,
// bypassing the store's schema bootstrap so tests can hand-build legacy shapes.
func execRaw(t *testing.T, dbPath string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	assert.NilError(t, err)
	defer db.Close()
	for _, s := range stmts {
		_, err := db.Exec(s)
		assert.NilError(t, err)
	}
}

// TestMigrateLegacyPreDBConfigDatabase covers the walker's legacy path: a
// database with CommandConfig but no DBConfig table is bootstrapped to version 1
// and migrated up to the chain's max version, after which the store opens and
// round-trips.
func TestMigrateLegacyPreDBConfigDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Build a pre-DBConfig v1 database: the 0001 schema minus the DBConfig table,
	// so CommandConfig exists without the CreatedAt column that 0002 adds.
	init := migration.All()[0]
	assert.Equal(t, init.Version, 1)
	execRaw(t, dbPath, init.SQL, `DROP TABLE DBConfig`)

	// Opening a legacy DB with validation tells the user to migrate.
	const wantLegacy = "database needs migration (no DBConfig table found), run 'cmdman migrate'"
	_, err := OpenStore(t.Context(), dbPath, true)
	assert.ErrorContains(t, err, wantLegacy)

	// The migrate command (validate=false) walks it up to the chain max version.
	st, err := OpenStore(t.Context(), dbPath, false)
	assert.NilError(t, err)
	assert.NilError(t, st.Migrate(t.Context()))

	var ver int
	err = st.DB().QueryRow(`SELECT SchemaVersion FROM DBConfig WHERE ID = 1`).Scan(&ver)
	assert.NilError(t, err)
	assert.Equal(t, ver, schemaVersion)
	assert.NilError(t, st.Close())

	// The migrated DB now opens with validation and round-trips a config (which
	// exercises the CreatedAt column added by 0002 during the migration).
	st2, err := OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	t.Cleanup(func() { st2.Close() })

	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: DefaultScrollbackBytes,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      "/tmp/cmd/legacy-1",
	}
	assert.NilError(t, st2.InsertCommandConfig("legacy-1", "legacy", cfg))
	id, name, got, err := st2.GetCommandConfig("legacy-1")
	assert.NilError(t, err)
	assert.Equal(t, id, "legacy-1")
	assert.Equal(t, name, "legacy")
	assert.Equal(t, got.Argv[0], "/bin/true")
}

// TestOpenStoreOutdatedSchemaVersion covers the validate path for a database
// stamped below the supported version: it must tell the user to run migrate.
func TestOpenStoreOutdatedSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "outdated.db")

	// A fresh DB migrates to the chain max; stamp an older version onto it.
	st, err := OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	_, err = st.DB().Exec(`UPDATE DBConfig SET SchemaVersion = 1 WHERE ID = 1`)
	assert.NilError(t, err)
	assert.NilError(t, st.Close())

	_, err = OpenStore(t.Context(), dbPath, true)
	assert.Assert(t, err != nil, "outdated DB should fail validation")
	want := fmt.Sprintf(
		"database schema version %d is outdated (current: %d), run 'cmdman migrate'",
		1, schemaVersion,
	)
	assert.ErrorContains(t, err, want)
}

// TestOpenStoreNewerThanSupportedVersion covers the validate path for a database
// stamped above the supported version.
func TestOpenStoreNewerThanSupportedVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "newer.db")

	st, err := OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	_, err = st.DB().Exec(`UPDATE DBConfig SET SchemaVersion = ? WHERE ID = 1`, schemaVersion+1)
	assert.NilError(t, err)
	assert.NilError(t, st.Close())

	_, err = OpenStore(t.Context(), dbPath, true)
	assert.Assert(t, err != nil, "newer-than-supported DB should fail validation")
	want := fmt.Sprintf(
		"database schema version %d is newer than supported version %d",
		schemaVersion+1, schemaVersion,
	)
	assert.ErrorContains(t, err, want)
}

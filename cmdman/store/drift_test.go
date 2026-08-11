package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// TestSchemaSQLMatchesMigrationChain is the D5 drift guard: sqlc reads the
// hand-maintained schema/schema.sql, while the runtime replays migration/*.sql.
// This test applies schema/schema.sql to one fresh database, replays the migration
// chain on another, and asserts the two produce byte-identical (normalized)
// sqlite_master table/index definitions. It fails loudly if either side changes alone.
func TestSchemaSQLMatchesMigrationChain(t *testing.T) {
	migrated := testStore(t) // OpenStore(validate=true) replays the migration chain.
	fromChain := dumpSchema(t, migrated.DB())

	data, err := os.ReadFile(filepath.Join("schema", "schema.sql"))
	assert.NilError(t, err)
	dbPath := filepath.Join(t.TempDir(), "schema.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	assert.NilError(t, err)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(string(data))
	assert.NilError(t, err)
	fromSchemaSQL := dumpSchema(t, db)

	assert.Equal(t, fromChain, fromSchemaSQL,
		"schema/schema.sql has drifted from migration/*.sql; keep them in sync (D5)")
}

// dumpSchema returns the normalized DDL of every user table and index, ordered
// deterministically. Whitespace and spacing around punctuation are collapsed so
// that formatting differences (e.g. an ALTER-appended column vs. an inline one)
// do not register as drift, while structural differences do.
func dumpSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(
		`SELECT type, name, sql FROM sqlite_master
		 WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'
		 ORDER BY type, name`,
	)
	assert.NilError(t, err)
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var typ, name, ddl string
		assert.NilError(t, rows.Scan(&typ, &name, &ddl))
		b.WriteString(typ)
		b.WriteByte(' ')
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(normalizeDDL(ddl))
		b.WriteByte('\n')
	}
	assert.NilError(t, rows.Err())
	return b.String()
}

var ddlSpaceAroundPunct = regexp.MustCompile(`\s*([(),])\s*`)

func normalizeDDL(ddl string) string {
	ddl = strings.Join(strings.Fields(ddl), " ")
	ddl = ddlSpaceAroundPunct.ReplaceAllString(ddl, "$1")
	return strings.TrimSpace(ddl)
}

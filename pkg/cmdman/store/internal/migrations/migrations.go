// Package migrations holds the ordered SQL schema migrations applied by
// the cmdman store on open.
package migrations

import "database/sql"

// SchemaMigrations maps a target schema version to the function that migrates
// from the previous version.
var SchemaMigrations = map[int]func(tx *sql.Tx) error{
	2: MigrateV1ToV2,
}

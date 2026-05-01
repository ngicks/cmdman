package migrations

import "database/sql"

// MigrateV1ToV2 adds the CreatedAt column to CommandConfig.
func MigrateV1ToV2(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE CommandConfig ADD COLUMN CreatedAt TEXT NOT NULL DEFAULT ''`)
	return err
}

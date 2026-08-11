package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ngicks/cmdman/cmdman/store/migration"
)

// schemaVersion is the highest schema version in the embedded migration chain.
// A schema change means adding a migration .sql file; the version is derived
// from the chain rather than maintained by hand.
var schemaVersion = migration.MaxVersion()

func validateDB(ctx context.Context, db *sql.DB) error {
	if err := initOrCheckSchema(ctx, db); err != nil {
		return err
	}
	if err := verifyJSONSupport(ctx, db); err != nil {
		return err
	}
	return nil
}

// initOrCheckSchema initializes the schema for a fresh DB or checks the
// schema version for an existing DB. Returns an error if migration is needed.
func initOrCheckSchema(ctx context.Context, db *sql.DB) error {
	exists, err := dbConfigExists(ctx, db)
	if err != nil {
		return err
	}
	if !exists {
		legacy, err := commandConfigExists(ctx, db)
		if err != nil {
			return err
		}
		if !legacy {
			return runMigrations(ctx, db)
		}
		return fmt.Errorf(
			"database needs migration (no DBConfig table found), run 'cmdman migrate'",
		)
	}

	ver, err := readSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if ver == schemaVersion {
		return nil
	}
	if ver > schemaVersion {
		return fmt.Errorf(
			"database schema version %d is newer than supported version %d",
			ver,
			schemaVersion,
		)
	}
	return fmt.Errorf(
		"database schema version %d is outdated (current: %d), run 'cmdman migrate'",
		ver,
		schemaVersion,
	)
}

func dbConfigExists(ctx context.Context, db *sql.DB) (bool, error) {
	return tableExists(ctx, db, "DBConfig")
}

func commandConfigExists(ctx context.Context, db *sql.DB) (bool, error) {
	return tableExists(ctx, db, "CommandConfig")
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var check int
	err := db.QueryRowContext(
		ctx,
		`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`,
		name,
	).Scan(&check)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking %s table: %w", name, err)
	}
	return check == 1, nil
}

func readSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var ver int
	err := db.QueryRowContext(ctx, `SELECT SchemaVersion FROM DBConfig WHERE ID = 1`).Scan(&ver)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if ver <= 0 {
		return 0, fmt.Errorf("invalid schema version: %d", ver)
	}
	return ver, nil
}

func verifyJSONSupport(ctx context.Context, db *sql.DB) error {
	var result string
	err := db.QueryRowContext(ctx, `SELECT json_extract('{"a":"b"}', '$.a')`).Scan(&result)
	if err != nil {
		return fmt.Errorf("SQLite JSON support unavailable: %w", err)
	}
	if result != "b" {
		return fmt.Errorf("SQLite JSON support broken: expected %q, got %q", "b", result)
	}
	return nil
}

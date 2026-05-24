package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// schemaVersion is the current schema version.
// Bump this and add a corresponding migration for each schema change.
const schemaVersion = 2

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
		// Fresh database or pre-DBConfig database.
		// Check if CommandConfig table exists (pre-DBConfig v1 database).
		var check int
		err := db.QueryRowContext(
			ctx,
			`SELECT 1 FROM sqlite_master WHERE type='table' AND name='CommandConfig'`,
		).Scan(&check)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking existing tables: %w", err)
		}
		if errors.Is(err, sql.ErrNoRows) || check != 1 {
			// Truly fresh database: create everything at current version.
			return createSchema(ctx, db)
		}
		// Pre-DBConfig database (v1): needs migration.
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

// createSchema creates all tables at the current schema version for a fresh database.
func createSchema(ctx context.Context, db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS DBConfig (
    ID            INTEGER PRIMARY KEY NOT NULL,
    SchemaVersion INTEGER NOT NULL,
    CHECK (ID IN (1))
);

CREATE TABLE IF NOT EXISTS CommandConfig (
    ID              TEXT PRIMARY KEY,
    Name            TEXT UNIQUE,
    CreatedAt       TEXT NOT NULL DEFAULT '',
    JSON            TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_command_config_name ON CommandConfig(Name);

CREATE TABLE IF NOT EXISTS CommandState (
    ID              TEXT PRIMARY KEY,
    State           TEXT NOT NULL,
    ExitCode        INTEGER CHECK (ExitCode BETWEEN -1 AND 255),
    JSON            TEXT NOT NULL,
    FOREIGN KEY (ID) REFERENCES CommandConfig(ID)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS idx_command_state_state ON CommandState(State);

CREATE TABLE IF NOT EXISTS CommandExitCode (
    ID              TEXT NOT NULL,
    Timestamp       TEXT NOT NULL,
    ExitCode        INTEGER NOT NULL CHECK (ExitCode BETWEEN -1 AND 255),
    FOREIGN KEY (ID) REFERENCES CommandConfig(ID)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS idx_command_exit_code_id_ts ON CommandExitCode(ID, Timestamp);
`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO DBConfig (ID, SchemaVersion) VALUES (1, ?)`,
		schemaVersion,
	); err != nil {
		return fmt.Errorf("insert DBConfig: %w", err)
	}
	return nil
}

func dbConfigExists(ctx context.Context, db *sql.DB) (bool, error) {
	var check int
	err := db.QueryRowContext(
		ctx,
		`SELECT 1 FROM sqlite_master WHERE type='table' AND name='DBConfig'`,
	).Scan(&check)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking DBConfig table: %w", err)
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

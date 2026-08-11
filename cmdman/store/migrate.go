package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ngicks/cmdman/cmdman/store/migration"
)

// Migrate runs all pending schema migrations.
func (s *Store) Migrate(ctx context.Context) error {
	return runMigrations(ctx, s.db)
}

func runMigrations(ctx context.Context, db *sql.DB) error {
	ver, err := currentSchemaVersion(ctx, db)
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

	for _, m := range migration.All() {
		if m.Version <= ver {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration executes a single migration and stamps its version inside one
// transaction. The DBConfig row is upserted so the first migration (which
// creates the DBConfig table) inserts it and later migrations update it.
func applyMigration(ctx context.Context, db *sql.DB, m migration.Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration to v%d: %w", m.Version, err)
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		tx.Rollback()
		return fmt.Errorf("migration to v%d: %w", m.Version, err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO DBConfig (ID, SchemaVersion) VALUES (1, ?)
			ON CONFLICT(ID) DO UPDATE SET SchemaVersion = excluded.SchemaVersion`,
		m.Version,
	); err != nil {
		tx.Rollback()
		return fmt.Errorf("update schema version to %d: %w", m.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration to v%d: %w", m.Version, err)
	}
	return nil
}

// currentSchemaVersion reports the version the migration chain should resume
// from: 0 for a fresh database (the whole chain replays), or the version stored
// in DBConfig. A pre-DBConfig legacy database (CommandConfig present, no
// DBConfig) is bootstrapped to version 1 so only later migrations replay.
func currentSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	exists, err := dbConfigExists(ctx, db)
	if err != nil {
		return 0, err
	}
	if exists {
		return readSchemaVersion(ctx, db)
	}

	legacy, err := commandConfigExists(ctx, db)
	if err != nil {
		return 0, err
	}
	if !legacy {
		// Truly fresh database: replay the whole chain from version 0.
		return 0, nil
	}
	if err := bootstrapLegacyDBConfig(ctx, db); err != nil {
		return 0, err
	}
	return 1, nil
}

// bootstrapLegacyDBConfig creates the DBConfig table and stamps version 1 for a
// pre-DBConfig database so the remaining migrations can run against it. Both
// statements run in one transaction: a crash between them would otherwise leave
// DBConfig existing but empty, wedging every future open on readSchemaVersion.
//
// The DDL mirrors DBConfig in migration/0001_init.sql; that file is the
// source of truth and TestSchemaSQLMatchesMigrationChain guards the final schema.
func bootstrapLegacyDBConfig(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy DBConfig bootstrap: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS DBConfig (
		ID            INTEGER PRIMARY KEY NOT NULL,
		SchemaVersion INTEGER NOT NULL,
		CHECK (ID IN (1))
	)`); err != nil {
		tx.Rollback()
		return fmt.Errorf("create DBConfig table: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO DBConfig (ID, SchemaVersion) VALUES (1, 1)`,
	); err != nil {
		tx.Rollback()
		return fmt.Errorf("insert initial DBConfig: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy DBConfig bootstrap: %w", err)
	}
	return nil
}

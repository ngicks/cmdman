package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman/store/internal/migrations"
)

// Migrate runs all pending schema migrations.
func (s *Store) Migrate(ctx context.Context) error {
	return runMigrations(ctx, s.db)
}

func runMigrations(ctx context.Context, db *sql.DB) error {
	exists, err := dbConfigExists(ctx, db)
	if err != nil {
		return err
	}

	if !exists {
		// Check if this is a pre-DBConfig database or truly fresh.
		var check int
		err := db.QueryRowContext(
			ctx,
			`SELECT 1 FROM sqlite_master WHERE type='table' AND name='CommandConfig'`,
		).Scan(&check)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking existing tables: %w", err)
		}
		if errors.Is(err, sql.ErrNoRows) || check != 1 {
			// Fresh database: create everything.
			return createSchema(ctx, db)
		}
		// Pre-DBConfig database. Create DBConfig at version 1, then migrate.
		if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS DBConfig (
			ID            INTEGER PRIMARY KEY NOT NULL,
			SchemaVersion INTEGER NOT NULL,
			CHECK (ID IN (1))
		)`); err != nil {
			return fmt.Errorf("create DBConfig table: %w", err)
		}
		if _, err := db.ExecContext(
			ctx,
			`INSERT INTO DBConfig (ID, SchemaVersion) VALUES (1, 1)`,
		); err != nil {
			return fmt.Errorf("insert initial DBConfig: %w", err)
		}
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

	for v := ver + 1; v <= schemaVersion; v++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		migrateFn, ok := migrations.SchemaMigrations[v]
		if !ok {
			return fmt.Errorf("no migration function for version %d", v)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration to v%d: %w", v, err)
		}
		if err := migrateFn(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration to v%d: %w", v, err)
		}
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE DBConfig SET SchemaVersion = ? WHERE ID = 1`,
			v,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("update schema version to %d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration to v%d: %w", v, err)
		}
	}
	return nil
}

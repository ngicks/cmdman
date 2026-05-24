// Package store is the SQLite-backed persistence layer for cmdman: it
// holds command definitions, runtime state, and exit history, and runs
// schema migrations on open.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	sqliteBusyTimeout       = 30 * time.Second
	openStoreMaxAttempts    = 4
	openStoreInitialBackoff = 100 * time.Millisecond
)

// Store provides access to the SQLite database for command management.
type Store struct {
	db *sql.DB
}

// OpenStore opens the SQLite database at the given path, configuring WAL mode,
// busy timeout, and foreign keys. If validate is true, it creates the schema
// for a fresh database or checks the schema version for an existing one,
// returning an error if migration is needed. Pass validate=false for the
// migrate command.
func OpenStore(ctx context.Context, dbPath string, validate bool) (*Store, error) {
	var lastErr error
	for attempt := range openStoreMaxAttempts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		store, err := openStore(ctx, dbPath)
		if err == nil && validate {
			if validateErr := validateDB(ctx, store.db); validateErr != nil {
				store.Close()
				err = validateErr
			}
		}
		if err == nil {
			return store, nil
		}
		if !isSQLiteBusyError(err) || attempt == openStoreMaxAttempts-1 {
			return nil, err
		}
		lastErr = err
		timer := time.NewTimer(openStoreInitialBackoff << attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func openStore(ctx context.Context, dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{db: db}, nil
}

// DB returns the underlying *sql.DB.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func sqliteDSN(dbPath string) string {
	u := url.URL{
		Scheme: "file",
		Path:   dbPath,
	}
	q := u.Query()
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeout.Milliseconds()))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	u.RawQuery = q.Encode()
	return u.String()
}

func isSQLiteBusyError(err error) bool {
	sqliteErr, ok := errors.AsType[*sqlite.Error](err)
	if !ok {
		return false
	}
	switch sqliteErr.Code() & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	default:
		return false
	}
}

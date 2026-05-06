// Package store is the SQLite-backed persistence layer for cmdman: it
// holds command definitions, runtime state, and exit history, and runs
// schema migrations on open.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store/internal/migrations"
	_ "modernc.org/sqlite"
)

// schemaVersion is the current schema version.
// Bump this and add a corresponding entry to schemaMigrations for each schema change.
const schemaVersion = 2

// Store provides access to the SQLite database for command management.
type Store struct {
	db *sql.DB
}

// OpenStore opens the SQLite database at the given path, configuring WAL mode,
// busy timeout, and foreign keys. If validate is true, it creates the schema
// for a fresh database or checks the schema version for an existing one,
// returning an error if migration is needed. Pass validate=false for the
// migrate command.
func OpenStore(dbPath string, validate bool) (*Store, error) {
	store, err := openStore(dbPath)
	if err != nil {
		return nil, err
	}
	if validate {
		if err := validateDB(store.db); err != nil {
			store.Close()
			return nil, err
		}
	}
	return store, nil
}

func openStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := configureDB(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func validateDB(db *sql.DB) error {
	if err := initOrCheckSchema(db); err != nil {
		return err
	}
	if err := verifyJSONSupport(db); err != nil {
		return err
	}
	return nil
}

// DB returns the underlying *sql.DB.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func configureDB(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("exec %q: %w", p, err)
		}
	}
	return nil
}

// initOrCheckSchema initializes the schema for a fresh DB or checks the
// schema version for an existing DB. Returns an error if migration is needed.
func initOrCheckSchema(db *sql.DB) error {
	exists, err := dbConfigExists(db)
	if err != nil {
		return err
	}
	if !exists {
		// Fresh database or pre-DBConfig database.
		// Check if CommandConfig table exists (pre-DBConfig v1 database).
		var check int
		err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='CommandConfig'`).
			Scan(&check)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking existing tables: %w", err)
		}
		if errors.Is(err, sql.ErrNoRows) || check != 1 {
			// Truly fresh database — create everything at current version.
			return createSchema(db)
		}
		// Pre-DBConfig database (v1) — needs migration.
		return fmt.Errorf(
			"database needs migration (no DBConfig table found), run 'cmdman migrate'",
		)
	}

	ver, err := readSchemaVersion(db)
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

// Migrate runs all pending schema migrations.
func (s *Store) Migrate() error {
	return runMigrations(s.db)
}

func runMigrations(db *sql.DB) error {
	exists, err := dbConfigExists(db)
	if err != nil {
		return err
	}

	if !exists {
		// Check if this is a pre-DBConfig database or truly fresh.
		var check int
		err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='CommandConfig'`).
			Scan(&check)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking existing tables: %w", err)
		}
		if errors.Is(err, sql.ErrNoRows) || check != 1 {
			// Fresh database — create everything.
			return createSchema(db)
		}
		// Pre-DBConfig database. Create DBConfig at version 1, then migrate.
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS DBConfig (
			ID            INTEGER PRIMARY KEY NOT NULL,
			SchemaVersion INTEGER NOT NULL,
			CHECK (ID IN (1))
		)`); err != nil {
			return fmt.Errorf("create DBConfig table: %w", err)
		}
		if _, err := db.Exec(`INSERT INTO DBConfig (ID, SchemaVersion) VALUES (1, 1)`); err != nil {
			return fmt.Errorf("insert initial DBConfig: %w", err)
		}
	}

	ver, err := readSchemaVersion(db)
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

	// Run migrations one version at a time.
	for v := ver + 1; v <= schemaVersion; v++ {
		migrateFn, ok := migrations.SchemaMigrations[v]
		if !ok {
			return fmt.Errorf("no migration function for version %d", v)
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration to v%d: %w", v, err)
		}
		if err := migrateFn(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration to v%d: %w", v, err)
		}
		if _, err := tx.Exec(`UPDATE DBConfig SET SchemaVersion = ? WHERE ID = 1`, v); err != nil {
			tx.Rollback()
			return fmt.Errorf("update schema version to %d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration to v%d: %w", v, err)
		}
	}
	return nil
}

func dbConfigExists(db *sql.DB) (bool, error) {
	var check int
	err := db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='DBConfig'`).
		Scan(&check)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking DBConfig table: %w", err)
	}
	return check == 1, nil
}

func readSchemaVersion(db *sql.DB) (int, error) {
	var ver int
	err := db.QueryRow(`SELECT SchemaVersion FROM DBConfig WHERE ID = 1`).Scan(&ver)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if ver <= 0 {
		return 0, fmt.Errorf("invalid schema version: %d", ver)
	}
	return ver, nil
}

// createSchema creates all tables at the current schema version for a fresh database.
func createSchema(db *sql.DB) error {
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
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	// Insert DBConfig row at current schema version.
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO DBConfig (ID, SchemaVersion) VALUES (1, ?)`,
		schemaVersion,
	); err != nil {
		return fmt.Errorf("insert DBConfig: %w", err)
	}
	return nil
}

func verifyJSONSupport(db *sql.DB) error {
	var result string
	err := db.QueryRow(`SELECT json_extract('{"a":"b"}', '$.a')`).Scan(&result)
	if err != nil {
		return fmt.Errorf("SQLite JSON support unavailable: %w", err)
	}
	if result != "b" {
		return fmt.Errorf("SQLite JSON support broken: expected %q, got %q", "b", result)
	}
	return nil
}

// Command states.
const (
	StateCreated  = "created"
	StateStarting = "starting"
	StateRunning  = "running"
	StateExited   = "exited"
	StateFailed   = "failed"
)

// InsertCommandConfig inserts a new CommandConfig row.
func (s *Store) InsertCommandConfig(id, name string, cfg *CommandConfigJSON) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO CommandConfig (ID, Name, CreatedAt, JSON) VALUES (?, ?, ?, ?)`,
		id, nullableString(name), time.Now().UTC().Format(time.RFC3339), string(data),
	)
	return err
}

// InsertCommandState inserts a new CommandState row.
func (s *Store) InsertCommandState(id, state string, stateJSON *CommandStateJSON) error {
	data, err := json.Marshal(stateJSON)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO CommandState (ID, State, ExitCode, JSON) VALUES (?, ?, NULL, ?)`,
		id, state, string(data),
	)
	return err
}

// UpdateCommandState updates the state and JSON of a CommandState row.
func (s *Store) UpdateCommandState(
	id, state string,
	exitCode *int,
	stateJSON *CommandStateJSON,
) error {
	data, err := json.Marshal(stateJSON)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE CommandState SET State = ?, ExitCode = ?, JSON = ? WHERE ID = ?`,
		state, exitCode, string(data), id,
	)
	return err
}

// InsertCommandExitCode records an exit code for a command.
func (s *Store) InsertCommandExitCode(id string, exitCode int) error {
	_, err := s.db.Exec(
		`INSERT INTO CommandExitCode (ID, Timestamp, ExitCode) VALUES (?, ?, ?)`,
		id, time.Now().UTC().Format(time.RFC3339), exitCode,
	)
	return err
}

// GetCommandConfig retrieves a CommandConfig by ID or name.
// GetCommandConfig retrieves a CommandConfig by exact name, exact ID, or ID prefix.
// If the input matches multiple commands by prefix, an error is returned.
func (s *Store) GetCommandConfig(
	idOrName string,
) (id, name string, cfg *CommandConfigJSON, err error) {
	resolvedID, err := s.ResolveID(idOrName)
	if err != nil {
		return "", "", nil, err
	}
	var nameSQL sql.NullString
	var jsonStr string
	err = s.db.QueryRow(
		`SELECT ID, Name, JSON FROM CommandConfig WHERE ID = ?`,
		resolvedID,
	).Scan(&id, &nameSQL, &jsonStr)
	if err != nil {
		return "", "", nil, err
	}
	if nameSQL.Valid {
		name = nameSQL.String
	}
	cfg = &CommandConfigJSON{}
	if err := json.Unmarshal([]byte(jsonStr), cfg); err != nil {
		return "", "", nil, err
	}
	backfillCommandConfigDefaults(cfg)
	return id, name, cfg, nil
}

// GetCommandState retrieves the CommandState for a command by ID.
func (s *Store) GetCommandState(
	id string,
) (state string, exitCode *int, stateJSON *CommandStateJSON, err error) {
	var ecSQL sql.NullInt64
	var jsonStr string
	err = s.db.QueryRow(
		`SELECT State, ExitCode, JSON FROM CommandState WHERE ID = ?`,
		id,
	).Scan(&state, &ecSQL, &jsonStr)
	if err != nil {
		return "", nil, nil, err
	}
	if ecSQL.Valid {
		ec := int(ecSQL.Int64)
		exitCode = &ec
	}
	stateJSON = &CommandStateJSON{}
	if err := json.Unmarshal([]byte(jsonStr), stateJSON); err != nil {
		return "", nil, nil, err
	}
	return state, exitCode, stateJSON, nil
}

// CommandEntry represents a joined row from CommandConfig and CommandState.
type CommandEntry struct {
	ID         string
	Name       string
	CreatedAt  string
	State      string
	ExitCode   *int
	ConfigJSON *CommandConfigJSON
	StateJSON  *CommandStateJSON
}

// ListCommands lists commands, optionally filtering by state and labels.
func (s *Store) ListCommands(allStates bool, labels map[string]string) ([]CommandEntry, error) {
	var query strings.Builder
	query.WriteString(`SELECT c.ID, c.Name, c.CreatedAt, s.State, s.ExitCode, c.JSON, s.JSON
		FROM CommandConfig c
		JOIN CommandState s ON c.ID = s.ID`)

	var args []any
	var conditions []string

	if !allStates {
		conditions = append(conditions, `s.State IN ('created', 'starting', 'running')`)
	}

	for k, v := range labels {
		conditions = append(conditions, `json_extract(c.JSON, '$.labels.' || ?) = ?`)
		args = append(args, k, v)
	}

	if len(conditions) > 0 {
		query.WriteString(" WHERE ")
		for i, c := range conditions {
			if i > 0 {
				query.WriteString(" AND ")
			}
			query.WriteString(c)
		}
	}

	query.WriteString(" ORDER BY c.CreatedAt")

	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []CommandEntry
	for rows.Next() {
		var e CommandEntry
		var nameSQL sql.NullString
		var ecSQL sql.NullInt64
		var cfgStr, stateStr string
		if err := rows.Scan(
			&e.ID,
			&nameSQL,
			&e.CreatedAt,
			&e.State,
			&ecSQL,
			&cfgStr,
			&stateStr,
		); err != nil {
			return nil, err
		}
		if nameSQL.Valid {
			e.Name = nameSQL.String
		}
		if ecSQL.Valid {
			ec := int(ecSQL.Int64)
			e.ExitCode = &ec
		}
		e.ConfigJSON = &CommandConfigJSON{}
		if err := json.Unmarshal([]byte(cfgStr), e.ConfigJSON); err != nil {
			return nil, err
		}
		backfillCommandConfigDefaults(e.ConfigJSON)
		e.StateJSON = &CommandStateJSON{}
		if err := json.Unmarshal([]byte(stateStr), e.StateJSON); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// GetExitHistory retrieves exit code history for a command.
func (s *Store) GetExitHistory(id string) ([]ExitRecord, error) {
	rows, err := s.db.Query(
		`SELECT Timestamp, ExitCode FROM CommandExitCode WHERE ID = ? ORDER BY Timestamp`,
		id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []ExitRecord
	for rows.Next() {
		var r ExitRecord
		if err := rows.Scan(&r.Timestamp, &r.ExitCode); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// ExitRecord represents an entry in CommandExitCode.
type ExitRecord struct {
	Timestamp string `json:"timestamp"`
	ExitCode  int    `json:"exit_code"`
}

// DeleteCommand removes all rows and the command directory for a command.
func (s *Store) DeleteCommand(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, table := range []string{"CommandExitCode", "CommandState", "CommandConfig"} {
		if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE ID = ?", table), id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ResolveID resolves an ID or name to a command ID.
// ResolveID resolves an ID prefix or exact name to a full command ID.
// If the input matches multiple commands by prefix, an error is returned.
func (s *Store) ResolveID(idOrName string) (string, error) {
	// Try exact name match first.
	var id string
	err := s.db.QueryRow(
		`SELECT ID FROM CommandConfig WHERE Name = ?`,
		idOrName,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	// Try exact ID match.
	err = s.db.QueryRow(
		`SELECT ID FROM CommandConfig WHERE ID = ?`,
		idOrName,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	// Try ID prefix match.
	rows, err := s.db.Query(
		`SELECT ID FROM CommandConfig WHERE ID LIKE ? || '%'`,
		idOrName,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var matches []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return "", err
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no command found matching %q", idOrName)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous ID prefix %q matches %d commands", idOrName, len(matches))
	}
}

// FindByLabels returns command IDs matching all the given labels.
func (s *Store) FindByLabels(labels map[string]string) ([]string, error) {
	var query strings.Builder
	query.WriteString(`SELECT ID FROM CommandConfig WHERE 1=1`)
	var args []any
	for k, v := range labels {
		query.WriteString(` AND json_extract(JSON, '$.labels.' || ?) = ?`)
		args = append(args, k, v)
	}
	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

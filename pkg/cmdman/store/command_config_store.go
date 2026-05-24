package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
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

// ResolveID resolves an ID prefix or exact name to a full command ID.
// If the input matches multiple commands by prefix, an error is returned.
func (s *Store) ResolveID(idOrName string) (string, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT ID FROM CommandConfig WHERE Name = ?`,
		idOrName,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	err = s.db.QueryRow(
		`SELECT ID FROM CommandConfig WHERE ID = ?`,
		idOrName,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

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

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

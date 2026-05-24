package store

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// CommandEntry represents a joined row from CommandConfig and CommandState.
type CommandEntry struct {
	ID         string
	Name       string
	CreatedAt  string
	State      string
	ExitCode   *int
	ConfigJSON *model.CommandConfigJSON
	StateJSON  *model.CommandStateJSON
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
		conditions = append(conditions, `json_extract(c.JSON, ?) = ?`)
		args = append(args, labelJSONPath(k), v)
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
		e.ConfigJSON = &model.CommandConfigJSON{}
		if err := json.Unmarshal([]byte(cfgStr), e.ConfigJSON); err != nil {
			return nil, err
		}
		backfillCommandConfigDefaults(e.ConfigJSON)
		e.StateJSON = &model.CommandStateJSON{}
		if err := json.Unmarshal([]byte(stateStr), e.StateJSON); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// FindByLabels returns command IDs matching all the given labels.
func (s *Store) FindByLabels(labels map[string]string) ([]string, error) {
	var query strings.Builder
	query.WriteString(`SELECT ID FROM CommandConfig WHERE 1=1`)
	var args []any
	for k, v := range labels {
		query.WriteString(` AND json_extract(JSON, ?) = ?`)
		args = append(args, labelJSONPath(k), v)
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

// labelJSONPath returns a SQLite json_extract path expression that selects
// the label whose key is k. Label keys can contain dots (e.g.
// "cmdman.compose.workdir"), which json_extract would otherwise interpret as
// nested property access. Quoting the key as $.labels."<k>" ensures it is
// treated as a single property name; any embedded double quotes inside k are
// escaped by doubling them, matching SQLite's quoting rules.
func labelJSONPath(k string) string {
	escaped := strings.ReplaceAll(k, `"`, `""`)
	return `$.labels."` + escaped + `"`
}

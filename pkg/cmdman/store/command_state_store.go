package store

import (
	"database/sql"
	"encoding/json"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// InsertCommandState inserts a new CommandState row.
func (s *Store) InsertCommandState(id string, state model.EventType, stateJSON *model.CommandState) error {
	data, err := json.Marshal(stateJSON)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO CommandState (ID, State, ExitCode, JSON) VALUES (?, ?, NULL, ?)`,
		id, string(state), string(data),
	)
	return err
}

// UpdateCommandState updates the state and JSON of a CommandState row.
func (s *Store) UpdateCommandState(
	id string,
	state model.EventType,
	exitCode *int,
	stateJSON *model.CommandState,
) error {
	data, err := json.Marshal(stateJSON)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`UPDATE CommandState SET State = ?, ExitCode = ?, JSON = ? WHERE ID = ?`,
		string(state), exitCode, string(data), id,
	)
	return err
}

// GetCommandState retrieves the CommandState for a command by ID.
func (s *Store) GetCommandState(
	id string,
) (state model.EventType, exitCode *int, stateJSON *model.CommandState, err error) {
	var ecSQL sql.NullInt64
	var stateStr string
	var jsonStr string
	err = s.db.QueryRow(
		`SELECT State, ExitCode, JSON FROM CommandState WHERE ID = ?`,
		id,
	).Scan(&stateStr, &ecSQL, &jsonStr)
	if err != nil {
		return "", nil, nil, err
	}
	state = model.EventType(stateStr)
	if ecSQL.Valid {
		ec := int(ecSQL.Int64)
		exitCode = &ec
	}
	stateJSON = &model.CommandState{}
	if err := json.Unmarshal([]byte(jsonStr), stateJSON); err != nil {
		return "", nil, nil, err
	}
	return state, exitCode, stateJSON, nil
}

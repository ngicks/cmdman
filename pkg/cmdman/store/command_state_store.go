package store

import (
	"database/sql"
	"encoding/json"
)

// Command states.
const (
	StateCreated  = "created"
	StateStarting = "starting"
	StateRunning  = "running"
	StateExited   = "exited"
	StateFailed   = "failed"
)

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

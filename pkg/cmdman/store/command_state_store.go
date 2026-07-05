package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store/gen/query"
)

// InsertCommandState inserts a new CommandState row.
func (s *Store) InsertCommandState(
	id string,
	state model.EventType,
	stateJSON *model.CommandState,
) error {
	data, err := json.Marshal(stateJSON)
	if err != nil {
		return err
	}
	return s.queries.InsertCommandState(context.Background(), query.InsertCommandStateParams{
		ID:    id,
		State: string(state),
		Json:  string(data),
	})
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
	return s.queries.UpdateCommandState(context.Background(), query.UpdateCommandStateParams{
		State:    string(state),
		Exitcode: nullableInt64(exitCode),
		Json:     string(data),
		ID:       id,
	})
}

// GetCommandState retrieves the CommandState for a command by ID.
func (s *Store) GetCommandState(
	id string,
) (state model.EventType, exitCode *int, stateJSON *model.CommandState, err error) {
	row, err := s.queries.GetCommandState(context.Background(), id)
	if err != nil {
		return "", nil, nil, err
	}
	state = model.EventType(row.State)
	if row.Exitcode.Valid {
		ec := int(row.Exitcode.Int64)
		exitCode = &ec
	}
	stateJSON = &model.CommandState{}
	if err := json.Unmarshal([]byte(row.Json), stateJSON); err != nil {
		return "", nil, nil, err
	}
	return state, exitCode, stateJSON, nil
}

// nullableInt64 mirrors database/sql's handling of a *int argument: a nil
// pointer becomes SQL NULL, a non-nil pointer its dereferenced value.
func nullableInt64(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

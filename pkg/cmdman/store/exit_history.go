package store

import (
	"context"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store/gen/query"
)

// InsertCommandExitCode records an exit code for a command.
func (s *Store) InsertCommandExitCode(id string, exitCode int) error {
	return s.queries.InsertCommandExitCode(
		context.Background(),
		query.InsertCommandExitCodeParams{
			ID:        id,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Exitcode:  int64(exitCode),
		},
	)
}

// ExitRecord represents an entry in CommandExitCode.
//
// Its fields are read from SQL columns (not a JSON blob); the only JSON
// rendering is as part of the CLI inspect output, which is consumed by Go
// `--format` templates. It therefore carries no json field-name tags, so
// `{{json .}}` and `{{.Field}}` agree on the Go field names.
type ExitRecord struct {
	Timestamp string
	ExitCode  int
}

// GetExitHistory retrieves exit code history for a command.
func (s *Store) GetExitHistory(id string) ([]ExitRecord, error) {
	rows, err := s.queries.GetExitHistory(context.Background(), id)
	if err != nil {
		return nil, err
	}
	var records []ExitRecord
	for _, r := range rows {
		records = append(records, ExitRecord{
			Timestamp: r.Timestamp,
			ExitCode:  int(r.Exitcode),
		})
	}
	return records, nil
}

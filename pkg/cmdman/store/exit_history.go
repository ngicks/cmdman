package store

import "time"

// InsertCommandExitCode records an exit code for a command.
func (s *Store) InsertCommandExitCode(id string, exitCode int) error {
	_, err := s.db.Exec(
		`INSERT INTO CommandExitCode (ID, Timestamp, ExitCode) VALUES (?, ?, ?)`,
		id, time.Now().UTC().Format(time.RFC3339), exitCode,
	)
	return err
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

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
type ExitRecord struct {
	Timestamp string `json:"timestamp"`
	ExitCode  int    `json:"exit_code"`
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

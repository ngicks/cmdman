package store

import "fmt"

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

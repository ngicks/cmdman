package store

import "context"

// DeleteCommand removes all rows and the command directory for a command.
func (s *Store) DeleteCommand(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ctx := context.Background()
	q := s.queries.WithTx(tx)
	if err := q.DeleteCommandExitCode(ctx, id); err != nil {
		return err
	}
	if err := q.DeleteCommandState(ctx, id); err != nil {
		return err
	}
	if err := q.DeleteCommandConfig(ctx, id); err != nil {
		return err
	}

	return tx.Commit()
}

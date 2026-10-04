package store

import (
	"context"

	"github.com/ngicks/cmdman/cmdman/store/gen/query"
)

// DeleteCommand removes all rows and the command directory for a command.
func (s *Store) DeleteCommand(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := deleteCommandRows(context.Background(), s.queries.WithTx(tx), id); err != nil {
		return err
	}

	return tx.Commit()
}

func deleteCommandRows(ctx context.Context, q *query.Queries, id string) error {
	if err := q.DeleteCommandExitCode(ctx, id); err != nil {
		return err
	}
	if err := q.DeleteCommandState(ctx, id); err != nil {
		return err
	}
	return q.DeleteCommandConfig(ctx, id)
}

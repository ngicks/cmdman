package store

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/cmdman/model"
)

// ReplaceCommand deletes every row of oldID and inserts newID, named name with
// cfg, in the created state. Both happen in one transaction: deleting first
// frees name for the UNIQUE constraint, and any failed insert rolls the deletes
// back, so oldID survives every error this returns.
func (s *Store) ReplaceCommand(oldID, newID, name string, cfg *model.CommandConfig) error {
	configParams, err := insertCommandConfigParams(newID, name, cfg)
	if err != nil {
		return err
	}
	stateParams, err := insertCommandStateParams(
		newID,
		model.EventTypeCreated,
		&model.CommandState{},
	)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ctx := context.Background()
	q := s.queries.WithTx(tx)
	if err := deleteCommandRows(ctx, q, oldID); err != nil {
		return fmt.Errorf("delete %s: %w", oldID, err)
	}
	if err := q.InsertCommandConfig(ctx, configParams); err != nil {
		return fmt.Errorf("insert config: %w", err)
	}
	if err := q.InsertCommandState(ctx, stateParams); err != nil {
		return fmt.Errorf("insert state: %w", err)
	}

	return tx.Commit()
}

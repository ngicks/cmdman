package cmdman

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// replaceOrInsertCommand stores id under name with cfg, replacing the stopped
// command already named name, or inserting as a plain create when there is none.
//
// Every step that can fail runs before the store swaps the rows, and the swap is
// a single transaction, so an error leaves the existing command as it was.
func (s *Service) replaceOrInsertCommand(
	ctx context.Context,
	st *store.Store,
	id, name string,
	cfg *model.CommandConfig,
) error {
	oldID, err := st.ResolveIDByName(name)
	if errors.Is(err, sql.ErrNoRows) {
		return insertCommand(st, id, name, cfg)
	}
	if err != nil {
		return fmt.Errorf("look up %q: %w", name, err)
	}

	state, _, _, err := st.GetCommandState(oldID)
	if err != nil {
		return fmt.Errorf("get state of %q: %w", name, err)
	}
	if state == model.EventTypeRunning || state == model.EventTypeStarting {
		return fmt.Errorf("command %q is %s, stop it before replacing", name, state)
	}
	_, _, oldCfg, err := st.GetCommandConfig(oldID)
	if err != nil {
		return fmt.Errorf("get config of %q: %w", name, err)
	}
	oldRuntimeDir, err := s.cfg.MonitorRuntimeDir(oldID)
	if err != nil {
		return err
	}

	// The new command directory is keyed by the fresh id, so writing it ahead
	// of the swap cannot disturb the command being replaced. Writing it after
	// the swap instead would leave a committed command without its config file
	// whenever the write fails.
	if err := store.WriteCommandConfig(cfg.CommandDir, cfg); err != nil {
		return fmt.Errorf("materialize config: %w", err)
	}
	if err := st.ReplaceCommand(oldID, id, name, cfg); err != nil {
		_ = os.RemoveAll(cfg.CommandDir)
		return fmt.Errorf("replace %q: %w", name, err)
	}

	if oldCfg.CommandDir != "" {
		_ = os.RemoveAll(oldCfg.CommandDir)
	}
	_ = os.RemoveAll(oldRuntimeDir)

	s.emitEvent(ctx, model.Event{
		Time: time.Now().UTC(),
		Type: model.EventTypeRemoved,
		ID:   oldID,
	})
	return nil
}

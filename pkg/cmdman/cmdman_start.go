package cmdman

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

func (s *Service) Start(ctx context.Context, idOrName string) error {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	id, _, cfg, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return fmt.Errorf("get command config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("get command config: %w", err)
	}

	state, _, _, err := st.GetCommandState(id)
	if err != nil {
		return fmt.Errorf("get command state: %w", err)
	}
	switch state {
	case store.StateCreated, store.StateExited, store.StateFailed:
	default:
		return fmt.Errorf(
			"command %s is in state %q, must be %q, %q, or %q",
			idOrName,
			state,
			store.StateCreated,
			store.StateExited,
			store.StateFailed,
		)
	}

	if _, err := SpawnMonitor(s.cfg, id); err != nil {
		return fmt.Errorf("spawn monitor: %w", err)
	}
	if finalState, err := WaitForState(st, id, store.StateRunning, 100); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if finalState == store.StateExited {
			return nil
		}
		return fmt.Errorf("%w (state: %s)", err, finalState)
	}
	return nil
}

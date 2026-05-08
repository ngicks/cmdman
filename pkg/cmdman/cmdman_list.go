package cmdman

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

func (s *Service) List(ctx context.Context, req ListRequest) ([]store.CommandEntry, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if err := CleanStaleEntries(st, s.cfg); err != nil {
		return nil, fmt.Errorf("clean stale entries: %w", err)
	}

	entries, err := st.ListCommands(req.AllStates, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	return entries, nil
}

package cmdman

import (
	"context"
	"fmt"
)

func (s *Service) Migrate(ctx context.Context) error {
	st, err := s.openStore(ctx, false)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

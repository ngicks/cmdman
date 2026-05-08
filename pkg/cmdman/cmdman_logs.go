package cmdman

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// Logs opens a structured reader for the persisted command output for
// req.IDOrName. With Follow=true, the reader tails the on-disk log file
// until ctx is cancelled. The monitor is not contacted; logs remain
// readable after the command exits.
func (s *Service) Logs(ctx context.Context, req LogsRequest) (logdriver.Reader, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	_, _, cfg, err := st.GetCommandConfig(req.IDOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	return logdriver.NewReader(ctx, cfg.LogDriver, cfg.LogPath(), req.Follow)
}

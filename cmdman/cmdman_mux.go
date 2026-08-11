package cmdman

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/cmdman/mux"
)

// MuxResolver returns a [mux.Resolver] for standalone `cmdman mux`, where a leaf
// names a concrete cmdman command (by ID or NAME). There is no replica cycling
// (the standalone case passes a nil ReplicaCounter), but a leaf may pin an
// explicit scale index, which resolves the suffixed command name
// "<leaf>-<scaleIndex>". The resolved command's ID is returned.
func (s *Service) MuxResolver() mux.Resolver {
	return func(ctx context.Context, leafName string, scaleIndex int) (string, error) {
		target := leafName
		if scaleIndex > 0 {
			target = fmt.Sprintf("%s-%d", leafName, scaleIndex)
		}
		out, err := s.Inspect(ctx, target)
		if err != nil {
			return "", err
		}
		return out.ID, nil
	}
}

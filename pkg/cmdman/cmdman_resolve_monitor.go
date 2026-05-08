package cmdman

import (
	"context"
	"fmt"
)

func (s *Service) ResolveMonitor(ctx context.Context, idOrName string) (*MonitorEndpoint, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	id, name, _, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	_, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return nil, fmt.Errorf("get state: %w", err)
	}
	if stateJSON.SocketPath == "" {
		return nil, fmt.Errorf("no socket path for command %s", id)
	}

	return &MonitorEndpoint{
		ID:         id,
		Name:       name,
		SocketPath: stateJSON.SocketPath,
	}, nil
}

package cmdman

import (
	"context"
	"fmt"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// InspectOutput is the merged command definition, state, and history.
type InspectOutput struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name,omitempty"`
	Config      *store.CommandConfigJSON `json:"config"`
	State       string                   `json:"state"`
	ExitCode    *int                     `json:"exit_code,omitempty"`
	StateJSON   *store.CommandStateJSON  `json:"state_detail"`
	ExitHistory []store.ExitRecord       `json:"exit_history,omitempty"`
	ConfigPath  string                   `json:"config_path,omitempty"`
	LiveStatus  *LiveStatusInfo          `json:"live_status,omitempty"`
}

// LiveStatusInfo is the live status from the monitor gRPC Status RPC.
type LiveStatusInfo struct {
	State    string `json:"state"`
	ExitCode int32  `json:"exit_code"`
	PID      int32  `json:"pid"`
}

func (s *Service) Inspect(ctx context.Context, idOrName string) (*InspectOutput, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	id, name, cfg, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	state, exitCode, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return nil, fmt.Errorf("get state: %w", err)
	}
	exitHistory, _ := st.GetExitHistory(id)

	out := &InspectOutput{
		ID:          id,
		Name:        name,
		Config:      cfg,
		State:       state,
		ExitCode:    exitCode,
		StateJSON:   stateJSON,
		ExitHistory: exitHistory,
		ConfigPath:  cfg.ConfigPath(),
	}

	if stateJSON.SocketPath != "" {
		if live := s.getLiveStatus(ctx, id); live != nil {
			out.LiveStatus = live
		}
	}
	return out, nil
}

func (s *Service) getLiveStatus(ctx context.Context, id string) *LiveStatusInfo {
	conn, err := s.connectMonitorByName(ctx, id)
	if err != nil {
		return nil
	}
	defer conn.Close()

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	resp, err := client.Status(ctx, &cmdmanv1pb.StatusRequest{})
	if err != nil {
		return nil
	}
	return &LiveStatusInfo{
		State:    resp.State,
		ExitCode: resp.ExitCode,
		PID:      resp.Pid,
	}
}

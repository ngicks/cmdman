package cmdman

import (
	"context"
	"fmt"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// InspectOutput is the merged command definition, state, and history.
//
// This is a CLI-output type: it is rendered as JSON by default and through
// `--format` Go templates, where users reference fields by their Go names
// (.ExitCode, .StateJSON). It therefore carries no json field-name tags, so the
// `{{json .}}` helper and `{{.Field}}` access agree on the same names. The
// nested Config/StateJSON/ExitHistory keep their own tags as documented on
// those types.
type InspectOutput struct {
	ID          string
	Name        string `json:",omitzero"`
	Config      *model.CommandConfig
	State       model.EventType
	ExitCode    *int `json:",omitzero"`
	StateJSON   *model.CommandState
	ExitHistory []store.ExitRecord `json:",omitzero"`
	ConfigPath  string             `json:",omitzero"`
	LiveStatus  *LiveStatusInfo    `json:",omitzero"`
}

// LiveStatusInfo is the live status from the monitor gRPC Status RPC.
// CLI-output type; see InspectOutput for why it carries no json name tags.
type LiveStatusInfo struct {
	State    string
	ExitCode int32
	PID      int32
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
		ConfigPath:  store.CommandConfigPath(cfg.CommandDir),
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

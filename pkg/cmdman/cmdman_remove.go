package cmdman

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// RemoveRequest defines a remove operation across explicit targets and/or labels.
type RemoveRequest struct {
	Targets []string
	Labels  map[string]string
	Force   bool
}

type RemoveResult struct {
	ID  string
	Err error
}

func (s *Service) Remove(ctx context.Context, req RemoveRequest) ([]RemoveResult, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	ids, err := resolveTargets(st, req.Targets, req.Labels)
	if err != nil {
		return nil, err
	}

	results := make([]RemoveResult, 0, len(ids))
	for _, id := range ids {
		err := rmOne(ctx, s.cfg, st, id, req.Force)
		results = append(results, RemoveResult{
			ID:  id,
			Err: err,
		})
		if err == nil {
			s.emitEvent(ctx, model.Event{
				Time: time.Now().UTC(),
				Type: model.EventTypeRemoved,
				ID:   id,
			})
		}
	}
	return results, nil
}

func rmOne(_ context.Context, cfg CmdmanConfig, st *store.Store, id string, force bool) error {
	state, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return err
	}

	if state == model.EventTypeRunning || state == model.EventTypeStarting {
		if !force {
			return fmt.Errorf("command is %s, use --force to remove", state)
		}
		if stateJSON.MonitorPID > 0 {
			proc, err := os.FindProcess(stateJSON.MonitorPID)
			if err == nil {
				_ = proc.Signal(syscall.SIGKILL)
			}
		}
	}

	_, _, commandCfg, err := st.GetCommandConfig(id)
	if err != nil {
		return err
	}

	if err := st.DeleteCommand(id); err != nil {
		return fmt.Errorf("delete from db: %w", err)
	}
	if commandCfg.CommandDir != "" {
		_ = os.RemoveAll(commandCfg.CommandDir)
	}
	runtimeDir, err := cfg.MonitorRuntimeDir(id)
	if err != nil {
		return err
	}
	_ = os.RemoveAll(runtimeDir)
	return nil
}

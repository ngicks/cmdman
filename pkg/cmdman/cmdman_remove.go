package cmdman

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

func (s *Service) Remove(ctx context.Context, req RemoveRequest) ([]CommandActionResult, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	ids, err := resolveTargets(st, req.Targets, req.Labels)
	if err != nil {
		return nil, err
	}

	results := make([]CommandActionResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, CommandActionResult{
			ID:  id,
			Err: rmOne(ctx, s.cfg, st, id, req.Force),
		})
	}
	return results, nil
}

func rmOne(_ context.Context, cfg CmdmanConfig, st *store.Store, id string, force bool) error {
	state, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return err
	}

	if state == store.StateRunning || state == store.StateStarting {
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

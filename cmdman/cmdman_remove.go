package cmdman

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
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
		err := s.rmOne(ctx, st, id, req.Force)
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

func (s *Service) rmOne(ctx context.Context, st *store.Store, id string, force bool) error {
	state, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return err
	}

	// Read before the stop below: a command created with auto-remove deletes
	// its own record once it stops, and its directory still has to go.
	_, _, commandCfg, err := st.GetCommandConfig(id)
	if err != nil {
		return err
	}

	if state == model.EventTypeRunning || state == model.EventTypeStarting {
		if !force {
			return fmt.Errorf("command is %s, use --force to remove", state)
		}
		if err := s.stopForRemoval(ctx, st, id, stateJSON.MonitorPID); err != nil {
			return err
		}
	}

	if err := st.DeleteCommand(id); err != nil {
		return fmt.Errorf("delete from db: %w", err)
	}
	if commandCfg.CommandDir != "" {
		_ = os.RemoveAll(commandCfg.CommandDir)
	}
	runtimeDir, err := s.cfg.MonitorRuntimeDir(id)
	if err != nil {
		return err
	}
	_ = os.RemoveAll(runtimeDir)
	return nil
}

// stopForRemoval stops id the way the stop verb does, so the monitor sweeps the
// processes the run left behind before the record goes. Killing the monitor
// first would orphan the command and everything it spawned. The removal goes
// ahead when the monitor does not answer or the stop fails. In that case
// stopForRemoval kills the monitor, so no monitor outlives its record. A
// cancelled ctx aborts the removal.
func (s *Service) stopForRemoval(
	ctx context.Context,
	st *store.Store,
	id string,
	monitorPID int,
) error {
	unreachable, err := s.stopReportingUnreachable(ctx, st, id, "", defaultStopTimeout)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("stop: %w", ctxErr)
	}
	if err == nil && !unreachable {
		return nil
	}
	if err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(ctx,
			"rm: stop through the monitor failed, killing the monitor",
			"id", id, "error", err)
	}
	if monitorPID > 0 {
		proc, err := os.FindProcess(monitorPID)
		if err == nil {
			_ = proc.Signal(syscall.SIGKILL)
		}
	}
	return nil
}

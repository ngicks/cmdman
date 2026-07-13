package monitor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/ngicks/cmdman/pkg/cmdman/config"
	"github.com/ngicks/cmdman/pkg/cmdman/internal/flock"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	cmdstore "github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

// CleanStaleEntries checks for stale monitors and marks them as failed.
func CleanStaleEntries(ctx context.Context, st *cmdstore.Store, cfg config.CmdmanConfig) error {
	entries, err := st.ListCommands(true, nil)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := cleanStaleEntry(
			ctx,
			st,
			cfg,
			e.ID,
			e.State,
			e.StateJSON,
			e.ConfigJSON,
		); err != nil {
			return err
		}
	}
	return nil
}

func cleanStaleEntry(
	ctx context.Context,
	st *cmdstore.Store,
	cfg config.CmdmanConfig,
	id string,
	state model.EventType,
	stateJSON *model.CommandState,
	configJSON *model.CommandConfig,
) error {
	if !isStaleCheckState(state) {
		return nil
	}

	stale, err := isStaleMonitor(cfg, id)
	if err != nil {
		// An unexpected probe error (permissions, I/O) must not be treated as
		// a dead monitor: skip the entry and let the ctx logger record it.
		contextkey.ValueSlogLoggerDefault(ctx).Warn(
			"clean stale entries: probe monitor liveness failed",
			slog.String("id", id),
			slog.String("error", err.Error()),
		)
		return nil
	}
	if !stale {
		return nil
	}

	return MarkMonitorDied(ctx, st, cfg, id, stateJSON, configJSON)
}

// MarkMonitorDied flips a command whose monitor has died to failed state,
// honoring auto-remove. It is exported for the Service stop path, which calls
// it when the monitor socket is unreachable.
func MarkMonitorDied(
	ctx context.Context,
	st *cmdstore.Store,
	cfg config.CmdmanConfig,
	id string,
	stateJSON *model.CommandState,
	configJSON *model.CommandConfig,
) error {
	stateJSON.Error = "monitor died unexpectedly"
	if err := st.UpdateCommandState(id, model.EventTypeFailed, nil, stateJSON); err != nil {
		return err
	}
	// Auto-remove if requested.
	if configJSON.Annotations[cmdstore.AnnotationAutoRemove] == "true" {
		if err := st.DeleteCommand(id); err != nil {
			return err
		}
		logger := contextkey.ValueSlogLoggerDefault(ctx)
		if err := os.RemoveAll(configJSON.CommandDir); err != nil {
			logger.Warn("auto-remove dir failed", slog.String("error", err.Error()))
		}
		runtimeDir, err := cfg.MonitorRuntimeDir(id)
		if err == nil {
			if err := os.RemoveAll(runtimeDir); err != nil {
				logger.Warn("auto-remove runtime dir failed", slog.String("error", err.Error()))
			}
		}
	}
	return nil
}

func isStaleCheckState(state model.EventType) bool {
	return state == model.EventTypeStarting || state == model.EventTypeRunning
}

// isStaleMonitor reports whether the monitor for id has died. It probes
// liveness by attempting a non-blocking exclusive flock on the monitor's PID
// file (cfg.MonitorPIDPath). A live monitor holds that lock for its whole
// lifetime (see Monitor.init), so:
//   - lock held by another process (try-lock busy) -> alive (not stale);
//   - lock acquired here, or the PID file is absent -> stale;
//   - unexpected open/flock errors are returned to the caller unclassified.
//
// Unlike a PID + Signal(0) check, this is immune to PID reuse: a recycled PID
// cannot resurrect a released advisory lock.
func isStaleMonitor(cfg config.CmdmanConfig, id string) (bool, error) {
	pidPath, err := cfg.MonitorPIDPath(id)
	if err != nil {
		return false, err
	}

	// Do not create the file: absence means the monitor already exited (it
	// removes its PID file on shutdown) and is therefore stale.
	f, err := os.OpenFile(pidPath, os.O_RDWR, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("open pid file %q: %w", pidPath, err)
	}
	defer f.Close()

	acquired, err := flock.TryLockExclusive(f)
	if err != nil {
		return false, fmt.Errorf("lock pid file %q: %w", pidPath, err)
	}
	if acquired {
		// No monitor holds the lock; release it and treat the entry as stale.
		_ = flock.Unlock(f)
		return true, nil
	}
	// The lock is busy, so a live monitor is holding it.
	return false, nil
}

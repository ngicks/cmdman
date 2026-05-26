package cmdman

import (
	"os"
	"syscall"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	cmdstore "github.com/ngicks/cmdman/pkg/cmdman/store"
)

// CheckMonitorAlive checks if a monitor process is still alive by PID.
func CheckMonitorAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// CleanStaleEntries checks for stale monitors and marks them as failed.
func CleanStaleEntries(st *cmdstore.Store, cfg CmdmanConfig) error {
	entries, err := st.ListCommands(true, nil)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := cleanStaleEntry(
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
	st *cmdstore.Store,
	cfg CmdmanConfig,
	id string,
	state model.EventType,
	stateJSON *model.CommandState,
	configJSON *model.CommandConfig,
) error {
	if !isStaleCheckState(state) || !isStaleMonitor(stateJSON) {
		return nil
	}

	return markMonitorDied(st, cfg, id, stateJSON, configJSON)
}

func markMonitorDied(
	st *cmdstore.Store,
	cfg CmdmanConfig,
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
		_ = os.RemoveAll(configJSON.CommandDir)
		runtimeDir, err := cfg.MonitorRuntimeDir(id)
		if err == nil {
			_ = os.RemoveAll(runtimeDir)
		}
	}
	return nil
}

func isStaleCheckState(state model.EventType) bool {
	return state == model.EventTypeStarting || state == model.EventTypeStarted
}

func isStaleMonitor(stateJSON *model.CommandState) bool {
	return stateJSON.MonitorPID > 0 && !CheckMonitorAlive(stateJSON.MonitorPID)
}

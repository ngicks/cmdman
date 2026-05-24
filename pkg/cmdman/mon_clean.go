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
		if e.State != model.StateStarting && e.State != model.StateRunning {
			continue
		}
		if e.StateJSON.MonitorPID > 0 && !CheckMonitorAlive(e.StateJSON.MonitorPID) {
			e.StateJSON.Error = "monitor died unexpectedly"
			_ = st.UpdateCommandState(e.ID, model.StateFailed, nil, e.StateJSON)

			// Auto-remove if requested.
			if e.ConfigJSON.Annotations[cmdstore.AnnotationAutoRemove] == "true" {
				_ = st.DeleteCommand(e.ID)
				_ = os.RemoveAll(e.ConfigJSON.CommandDir)
				runtimeDir, err := cfg.MonitorRuntimeDir(e.ID)
				if err == nil {
					_ = os.RemoveAll(runtimeDir)
				}
			}
		}
	}
	return nil
}

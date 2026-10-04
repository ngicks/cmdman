package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// This file holds the platform-independent parts of monitor spawning. The
// OS-specific detach strategy lives in mon_spawn_<os>.go (currently only the
// POSIX double-fork in mon_spawn_posix.go): SpawnMonitor and DaemonizeMonitor
// are defined there so a Windows implementation can be added as a sibling file
// with no change to this one.

// newMonitorCmd builds an exec.Cmd that re-runs the current binary's hidden
// __monitor command for id. extraEnv is appended to the inherited environment;
// pass nil to inherit it unchanged.
//
// cfg is the caller's already-resolved configuration; its dirs are passed to the
// child as flags so the monitor supervises the same store no matter what the
// child's own environment would have resolved to. cfg.ConfigPath is forwarded
// the same way when set: the child inherits $CMDMAN_CONF but not --config, and
// without it the file-only settings the monitor consumes (config.DefaultHooks)
// would silently fall back to the default location.
func newMonitorCmd(cfg config.Config, id string, extraEnv []string) (*exec.Cmd, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	args := []string{
		"--data-dir", cfg.DataDir,
		"--runtime-dir", cfg.RuntimeDir,
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--config", cfg.ConfigPath)
	}
	args = append(args, "__monitor", "--id", id)

	cmd := exec.Command(exe, args...)
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	return cmd, nil
}

// StateMark is a command's state record as read right before a monitor is
// spawned for it. [WaitForState] tells the spawned run apart from it.
type StateMark struct {
	State model.EventType
	// MonitorPID is the PID of the monitor that last wrote the record, or 0.
	MonitorPID int
}

// WaitForState polls the store until the command reaches the desired state
// or the timeout is reached. Returns the final state observed.
//
// since is the record before the spawn. The state has progressed once it
// differs from since, or once a monitor other than since's has written it,
// which a new monitor does with its first write. A leftover EventTypeFailed or
// EventTypeExited (e.g. when restarting a previously stopped command) is
// therefore not treated as the end of a new run. Once the state has
// progressed, a transition into EventTypeFailed is reported as an error and a
// transition into EventTypeExited returns that state with no error: a run that
// has already ended can no longer reach the desired state, and polling on
// would only wait out the timeout.
func WaitForState(
	st *store.Store,
	id string,
	desiredState model.EventType,
	since StateMark,
	maxAttempts int,
) (model.EventType, error) {
	progressed := false
	for range maxAttempts {
		state, _, stateJSON, err := st.GetCommandState(id)
		if err != nil {
			return "", err
		}
		if state != since.State || stateJSON.MonitorPID != since.MonitorPID {
			progressed = true
		}
		if state == desiredState {
			return state, nil
		}
		if progressed {
			switch state {
			case model.EventTypeFailed:
				return state, fmt.Errorf("monitor entered failed state")
			case model.EventTypeExited:
				return state, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	state, _, _, _ := st.GetCommandState(id)
	return state, fmt.Errorf("timeout waiting for state %q, last state: %q", desiredState, state)
}

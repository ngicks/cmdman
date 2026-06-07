package cmdman

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// This file holds the platform-independent parts of monitor spawning. The
// OS-specific detach strategy lives in mon_spawn_<os>.go (currently only the
// POSIX double-fork in mon_spawn_posix.go): SpawnMonitor and DaemonizeMonitor
// are defined there so a Windows implementation can be added as a sibling file
// with no change to this one.

// newMonitorCmd builds an exec.Cmd that re-runs the current binary's hidden
// __monitor command for id. extraEnv is appended to the inherited environment;
// pass nil to inherit it unchanged.
func newMonitorCmd(cfg CmdmanConfig, id string, extraEnv []string) (*exec.Cmd, error) {
	commandCfg, err := cfg.WithDefaults()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.Command(exe,
		"--data-dir", commandCfg.DataDir,
		"--runtime-dir", commandCfg.RuntimeDir,
		"__monitor",
		"--id", id,
	)
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	return cmd, nil
}

// WaitForState polls the store until the command reaches the desired state
// or the timeout is reached. Returns the final state observed.
//
// When the initial observation is EventTypeFailed (e.g. when restarting a
// previously failed command), the leftover state is not treated as a new
// failure; only a transition into EventTypeFailed after the state has progressed
// is reported as such.
func WaitForState(
	st *store.Store,
	id string,
	desiredState model.EventType,
	maxAttempts int,
) (model.EventType, error) {
	var initial model.EventType
	progressed := false
	for i := range maxAttempts {
		state, _, _, err := st.GetCommandState(id)
		if err != nil {
			return "", err
		}
		if i == 0 {
			initial = state
		}
		if state != initial {
			progressed = true
		}
		if state == desiredState {
			return state, nil
		}
		if state == model.EventTypeFailed && progressed {
			return state, fmt.Errorf("monitor entered failed state")
		}
		time.Sleep(50 * time.Millisecond)
	}
	state, _, _, _ := st.GetCommandState(id)
	return state, fmt.Errorf("timeout waiting for state %q, last state: %q", desiredState, state)
}

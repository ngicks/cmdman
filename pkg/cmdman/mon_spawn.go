package cmdman

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// SpawnMonitor starts the monitor as a detached process via re-exec.
// It launches the current executable with the __monitor subcommand.
func SpawnMonitor(cfg CmdmanConfig, id string) (*os.Process, error) {
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

	clean, err := detachProcess(cmd)
	if err != nil {
		return nil, err
	}
	defer clean()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start monitor: %w", err)
	}

	// Release the child so it runs independently.
	proc := cmd.Process
	cmd.Process.Release()

	return proc, nil
}

// WaitForState polls the store until the command reaches the desired state
// or the timeout is reached. Returns the final state observed.
//
// When the initial observation is StateFailed (e.g. when restarting a
// previously failed command), the leftover state is not treated as a new
// failure; only a transition into StateFailed after the state has progressed
// is reported as such.
func WaitForState(st *store.Store, id, desiredState string, maxAttempts int) (string, error) {
	var initial string
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
		if state == model.StateFailed && progressed {
			return state, fmt.Errorf("monitor entered failed state")
		}
		time.Sleep(50 * time.Millisecond)
	}
	state, _, _, _ := st.GetCommandState(id)
	return state, fmt.Errorf("timeout waiting for state %q, last state: %q", desiredState, state)
}

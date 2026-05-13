//go:build !plan9 && !windows && !wasm

package cmdman

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func detachProcess(cmd *exec.Cmd) (clean func() error, err error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	// Redirect stdin/stdout/stderr to /dev/null.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, fmt.Errorf("open /dev/null: %w", err)
	}

	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	return devNull.Close, nil
}

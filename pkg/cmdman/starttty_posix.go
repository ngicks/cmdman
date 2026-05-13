//go:build !plan9 && !windows && !wasm

package cmdman

import (
	"os"
	"os/exec"

	"github.com/creack/pty/v2"
)

func startTty(cmd *exec.Cmd) (*os.File, error) {
	return pty.Start(cmd)
}

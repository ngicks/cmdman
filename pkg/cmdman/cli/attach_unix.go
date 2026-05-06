package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

func terminalSize(stdout *os.File) (rows, cols int) {
	ws, err := unix.IoctlGetWinsize(int(stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0
	}
	return int(ws.Row), int(ws.Col)
}

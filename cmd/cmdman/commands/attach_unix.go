package commands

import (
	"os"

	"golang.org/x/sys/unix"
)

func getTerminalSizeImpl() (rows, cols int) {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0
	}
	return int(ws.Row), int(ws.Col)
}

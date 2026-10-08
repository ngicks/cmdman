package cli

import (
	"fmt"
	"io"

	"github.com/ngicks/cmdman/cmdman"
)

// forceKilledNote is what the CLI says about a command whose stop ran out its
// grace period and ended it with SIGKILL. That SIGKILL reaches the command's
// process group and, on Linux, what is left in the command's session. A
// process that started a session of its own is out of its reach.
const forceKilledNote = "force-killed after the grace period; detached processes may survive"

// PrintStopForceKilled writes one line to errOut for each stop in results that
// force-killed its command.
func PrintStopForceKilled(errOut io.Writer, results []cmdman.StopResult) {
	for _, r := range results {
		if r.ForceKilled {
			fmt.Fprintf(errOut, "stop %s: %s\n", r.ID, forceKilledNote)
		}
	}
}

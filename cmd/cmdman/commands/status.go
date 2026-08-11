package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
)

func statusCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Read and write the status a command reports about itself",
		Long: `Read and write the status a command reports about itself.

The status is one of working, waiting or done, plus an optional detail. It is
held by the monitor of the current run: it is cleared when the command restarts
and is gone once the command exits.

Every command cmdman supervises gets CMDMAN_CMD_ID in its environment, so a
command reports about itself with no argument at all:

  cmdman status set waiting --detail "needs input"

From outside, name the command instead:

  cmdman status get my-command`,
	}

	statusSetCmd(cmd, rootCfg)
	statusGetCmd(cmd, rootCfg)
	statusDeleteCmd(cmd, rootCfg)

	parent.AddCommand(cmd)
}

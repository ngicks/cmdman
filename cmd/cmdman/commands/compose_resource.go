package commands

import (
	"github.com/spf13/cobra"
)

func composeResourceCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	cmd := &cobra.Command{
		Use:   "resource",
		Short: "Read and write the resource values of compose commands",
		Long: `Read and write the resource values of compose commands.

A hook item with a resource key stores what its acquire event printed last on
stdout as the value of that resource, one value per replica. Its release event
reads the value back from CMDMAN_COMPOSE_RESOURCE_VALUE.

The value stays readable after the replica is removed, until the release event
or unset removes it.

Without selection flags the project is taken from CMDMAN_COMPOSE_WORK_DIR and
CMDMAN_COMPOSE_PROJECT, which compose sets for every replica and hook, and
otherwise from the compose file in the current directory. Inside a replica or
hook of COMMAND, --scale defaults to its own scale index:

  cmdman compose resource get web scratch`,
	}

	composeResourceGetCmd(cmd, rf, cf)
	composeResourceSetCmd(cmd, rf, cf)
	composeResourceUnsetCmd(cmd, rf, cf)

	parent.AddCommand(cmd)
}

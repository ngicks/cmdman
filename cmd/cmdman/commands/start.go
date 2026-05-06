package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func startCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	cmd := &cobra.Command{
		Use:   "start ID_OR_NAME",
		Short: "Start a created command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, args, rootCfg)
		},
	}

	parent.AddCommand(cmd)
}

func runStart(cmd *cobra.Command, args []string, rootCfg *cmdman.CmdmanConfig) error {
	return doStart(cmd, args[0], rootCfg)
}

// doStart spawns the monitor for an existing command in "created" state.
func doStart(cmd *cobra.Command, idOrName string, rootCfg *cmdman.CmdmanConfig) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	return svc.Start(cmd.Context(), idOrName)
}

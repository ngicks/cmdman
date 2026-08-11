package commands

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

func statusDeleteCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	cmd := &cobra.Command{
		Use:   "delete [ID|NAME]",
		Short: "Clear the status a command reports about itself",
		Long: `Clear the status a command reports about itself.

Without ID|NAME the command is taken from CMDMAN_CMD_ID, which cmdman sets in
every command it supervises. The command must be running: the status lives in
the monitor of the current run.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeCommandNames(rootCfg, runningStates...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatusDelete(cmd, args, rootCfg)
		},
	}

	parent.AddCommand(cmd)
}

func runStatusDelete(cmd *cobra.Command, args []string, rootCfg *cmdman.CmdmanConfig) error {
	target, err := cli.ResolveStatusTarget(args, os.Getenv(cmdman.ENV_CMDMAN_CMD_ID))
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	return svc.DeleteReportedStatus(cmd.Context(), target)
}

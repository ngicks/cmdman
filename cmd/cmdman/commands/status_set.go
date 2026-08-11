package commands

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

func statusSetCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagDetail string
	)

	cmd := &cobra.Command{
		Use:   "set working|waiting|done [ID|NAME]",
		Short: "Set the status a command reports about itself",
		Long: `Set the status a command reports about itself, replacing any previous one.

Without ID|NAME the command is taken from CMDMAN_CMD_ID, which cmdman sets in
every command it supervises. The command must be running: the status lives in
the monitor of the current run.`,
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: completeReportedStatus(rootCfg),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatusSet(cmd, args, rootCfg, flagDetail)
		},
	}

	cmd.Flags().StringVar(
		&flagDetail, "detail", "",
		"Free-form detail shown beside the status; omitting it on a later set clears it",
	)

	parent.AddCommand(cmd)
}

func runStatusSet(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	detail string,
) error {
	target, err := cli.ResolveStatusTarget(args[1:], os.Getenv(cmdman.ENV_CMDMAN_CMD_ID))
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	return svc.SetReportedStatus(cmd.Context(), target, args[0], detail)
}

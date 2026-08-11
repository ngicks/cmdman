package commands

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/cli"
)

func statusGetCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:   "get [ID|NAME]",
		Short: "Print the status a command reports about itself",
		Long: `Print the status a command reports about itself.

Without ID|NAME the command is taken from CMDMAN_CMD_ID, which cmdman sets in
every command it supervises. A command that reported nothing - and one that is
not running, which can hold no status - prints nothing.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeCommandNames(rootCfg),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatusGet(cmd, args, rootCfg, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.StatusFormatUsage())

	parent.AddCommand(cmd)
}

func runStatusGet(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	format string,
) error {
	target, err := cli.ResolveStatusTarget(args, os.Getenv(cmdman.ENV_CMDMAN_CMD_ID))
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	info, err := svc.GetReportedStatus(cmd.Context(), target)
	if err != nil {
		return err
	}
	return cli.RenderReportedStatus(cmd.OutOrStdout(), info, format)
}

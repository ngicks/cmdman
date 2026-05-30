package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

func inspectCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:               "inspect ID|NAME",
		Short:             "Show merged command definition, runtime state, and exit history",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCommandNames(rootCfg),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd, args, rootCfg, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.InspectFormatUsage())

	parent.AddCommand(cmd)
}

func runInspect(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	format string,
) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	out, err := svc.Inspect(cmd.Context(), args[0])
	if err != nil {
		return err
	}

	return cli.RenderInspect(cmd.OutOrStdout(), out, format)
}

package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeInspectCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:               "inspect [COMMAND...]",
		Short:             "Show merged definition, state, and exit history for compose commands",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rootCfg, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeInspect(cmd, rootCfg, cf, args, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.InspectFormatUsage())

	parent.AddCommand(cmd)
}

func runComposeInspect(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	format string,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	outputs, err := compose.NewService(svc).Inspect(cmd.Context(), selection, commandNames)
	if err != nil {
		return err
	}

	return cli.RenderComposeInspect(cmd.OutOrStdout(), outputs, format)
}

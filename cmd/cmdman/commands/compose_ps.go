package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composePsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:               "ps [COMMAND...]",
		Short:             "List commands in a compose project",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rootCfg, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposePs(cmd, rootCfg, cf, args, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.ComposePsFormatUsage())

	parent.AddCommand(cmd)
}

func runComposePs(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	format string,
) error {
	selection, err := compose.LoadOrWorkdir(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	statuses, err := compose.NewService(svc).Ps(cmd.Context(), selection, commandNames)
	if err != nil {
		return err
	}

	return cli.RenderComposePs(cmd.OutOrStdout(), statuses, format)
}

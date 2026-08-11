package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeInspectCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:               "inspect [COMMAND...]",
		Short:             "Show merged definition, state, and exit history for compose commands",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeInspect(cmd, rf, cf, args, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.InspectFormatUsage())

	parent.AddCommand(cmd)
}

func runComposeInspect(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	format string,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
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

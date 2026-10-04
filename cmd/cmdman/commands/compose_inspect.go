package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeInspectCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagFormat string
		flagScale  int
	)

	cmd := &cobra.Command{
		Use:               "inspect [COMMAND...]",
		Short:             "Show merged definition, state, and exit history for compose commands",
		Args:              scaleArgs(cobra.ArbitraryArgs, &flagScale),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeInspect(cmd, rf, cf, args, flagScale, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.InspectFormatUsage())
	addScaleFlag(cmd, &flagScale)

	parent.AddCommand(cmd)
}

func runComposeInspect(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	scale int,
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

	outputs, err := compose.NewService(svc).Inspect(cmd.Context(), selection, compose.InspectOption{
		Targets: composeTargets(commandNames, scale),
	})
	if err != nil {
		return err
	}

	return cli.RenderComposeInspect(cmd.OutOrStdout(), outputs, format)
}

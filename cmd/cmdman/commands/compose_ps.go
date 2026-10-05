package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composePsCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagFormat string
		flagScale  int
	)

	cmd := &cobra.Command{
		Use:               "ps [COMMAND...]",
		Short:             "List commands in a compose project",
		Args:              scaleArgs(cobra.ArbitraryArgs, &flagScale),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposePs(cmd, rf, cf, args, flagScale, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.ComposePsFormatUsage())
	addScaleFlag(cmd, &flagScale)

	parent.AddCommand(cmd)
}

func runComposePs(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	scale int,
	format string,
) error {
	selection, err := compose.LoadOrWorkdir(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	statuses, err := compose.NewService(svc).Ps(cmd.Context(), selection, compose.PsOption{
		Targets: composeTargets(commandNames, scale),
	})
	if err != nil {
		return err
	}

	return cli.RenderComposePs(cmd.OutOrStdout(), statuses, format)
}

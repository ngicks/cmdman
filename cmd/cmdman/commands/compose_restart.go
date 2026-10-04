package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeRestartCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagScale int
	)

	cmd := &cobra.Command{
		Use:               "restart [COMMAND...]",
		Short:             "Stop then start compose commands",
		Args:              scaleArgs(cobra.ArbitraryArgs, &flagScale),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeRestart(cmd, rf, cf, args, flagScale)
		},
	}

	addScaleFlag(cmd, &flagScale)

	parent.AddCommand(cmd)
}

func runComposeRestart(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	scale int,
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

	result, err := compose.NewService(svc).Restart(cmd.Context(), selection, compose.RestartOption{
		Targets: composeTargets(commandNames, scale),
	})
	if err != nil {
		return err
	}

	return cli.PrintRestartResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Restarts)
}

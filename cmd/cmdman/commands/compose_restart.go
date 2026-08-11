package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeRestartCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	cmd := &cobra.Command{
		Use:               "restart [COMMAND...]",
		Short:             "Stop then start compose commands",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeRestart(cmd, rf, cf, args)
		},
	}

	parent.AddCommand(cmd)
}

func runComposeRestart(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
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
		CommandNames: commandNames,
	})
	if err != nil {
		return err
	}

	return cli.PrintRestartResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Restarts)
}

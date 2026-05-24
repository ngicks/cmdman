package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeRestartCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	cmd := &cobra.Command{
		Use:   "restart [COMMAND...]",
		Short: "Stop then start compose commands",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeRestart(cmd, rootCfg, cf, args)
		},
	}

	parent.AddCommand(cmd)
}

func runComposeRestart(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
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

	result, err := compose.NewService(svc).Restart(cmd.Context(), selection, compose.RestartOption{
		CommandNames: commandNames,
	})
	if err != nil {
		return err
	}

	return cli.PrintRestartResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Restarts)
}

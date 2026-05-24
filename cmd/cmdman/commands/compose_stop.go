package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeStopCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	cmd := &cobra.Command{
		Use:   "stop [COMMAND...]",
		Short: "Stop running compose commands",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeStop(cmd, rootCfg, cf, args)
		},
	}

	parent.AddCommand(cmd)
}

func runComposeStop(
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

	result, err := compose.NewService(svc).Stop(cmd.Context(), selection, compose.StopOption{
		CommandNames: commandNames,
	})
	if err != nil {
		return err
	}

	return cli.PrintStopResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Stops)
}

package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeDownCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	cmd := &cobra.Command{
		Use:   "down [COMMAND...]",
		Short: "Stop and remove compose commands",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeDown(cmd, rootCfg, cf, args)
		},
	}

	parent.AddCommand(cmd)
}

func runComposeDown(
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

	result, err := compose.NewService(svc).Down(cmd.Context(), selection, compose.DownOption{
		CommandNames: commandNames,
	})
	if err != nil {
		return err
	}

	return cli.PrintDownResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result)
}

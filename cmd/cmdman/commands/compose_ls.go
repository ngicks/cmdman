package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeLsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List compose projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeLs(cmd, rootCfg)
		},
	}

	parent.AddCommand(cmd)
}

func runComposeLs(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	summaries, err := compose.NewService(svc).ListProjects(cmd.Context())
	if err != nil {
		return err
	}

	cli.PrintComposeProjects(cmd.OutOrStdout(), summaries)
	return nil
}

package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func migrateCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	cmd := &cobra.Command{
		Use:               "migrate",
		Short:             "Run database schema migrations",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate(cmd, args, rootCfg)
		},
	}

	parent.AddCommand(cmd)
}

func runMigrate(cmd *cobra.Command, _ []string, rootCfg *cmdman.CmdmanConfig) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	if err := svc.Migrate(cmd.Context()); err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), "migrations complete")
	return nil
}

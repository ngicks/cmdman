package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func migrateCmd(parent *cobra.Command, rf *rootFlags) {
	cmd := &cobra.Command{
		Use:               "migrate",
		Short:             "Run database schema migrations",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate(cmd, args, rf)
		},
	}

	parent.AddCommand(cmd)
}

func runMigrate(cmd *cobra.Command, _ []string, rf *rootFlags) error {
	svc, err := cmdmanService(cmd, rf)
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

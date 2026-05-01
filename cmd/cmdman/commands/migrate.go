package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(migrateCmd)
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database schema migrations",
	RunE:  runMigrate,
}

func runMigrate(cmd *cobra.Command, args []string) error {
	svc, err := cmdmanService()
	if err != nil {
		return err
	}
	if err := svc.Migrate(cmd.Context()); err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), "migrations complete")
	return nil
}

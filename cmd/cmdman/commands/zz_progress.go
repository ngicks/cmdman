package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

// resolveComposeProgress parses the --progress flag value and constructs the
// state-trace reporter for op, targeting the command's stdout. The caller must
// Close the returned reporter once the operation finishes.
func resolveComposeProgress(cmd *cobra.Command, progress, op string) (cli.ComposeProgress, error) {
	mode, err := cli.ParseProgressMode(progress)
	if err != nil {
		return nil, err
	}
	return cli.NewComposeProgress(cmd.OutOrStdout(), mode, op), nil
}

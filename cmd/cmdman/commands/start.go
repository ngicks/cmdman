package commands

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start ID_OR_NAME",
	Short: "Start a created command",
	Args:  cobra.ExactArgs(1),
	RunE:  runStart,
}

func runStart(cmd *cobra.Command, args []string) error {
	return doStart(cmd, args[0])
}

// doStart spawns the monitor for an existing command in "created" state.
func doStart(cmd *cobra.Command, idOrName string) error {
	svc, err := cmdmanService()
	if err != nil {
		return err
	}
	return svc.Start(cmd.Context(), idOrName)
}

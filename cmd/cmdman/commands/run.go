package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(runCmd)
	addCreateFlags(runCmd)
	runCmd.Flags().Bool("attach", false, "Attach after the command reaches running")
}

var runCmd = &cobra.Command{
	Use:   "run [flags] -- COMMAND [ARGS...]",
	Short: "Create and start a new command",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRun,
}

func runRun(cmd *cobra.Command, args []string) error {
	attach, _ := cmd.Flags().GetBool("attach")

	id, name, err := doCreate(cmd, args)
	if err != nil {
		return err
	}

	if err := doStart(cmd, id); err != nil {
		return err
	}

	displayName := id
	if name != "" {
		displayName = name
	}
	fmt.Fprintln(cmd.OutOrStdout(), displayName)

	if attach {
		svc, err := cmdmanService()
		if err != nil {
			return err
		}
		endpoint, err := svc.ResolveMonitor(cmd.Context(), id)
		if err != nil {
			return err
		}
		if endpoint.SocketPath != "" {
			return runAttach(cmd, id)
		}
	}

	return nil
}

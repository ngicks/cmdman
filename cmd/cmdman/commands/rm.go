package commands

import (
	"fmt"

	"github.com/ngicks/crabswarm/pkg/cmdman"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(rmCmd)
	rmCmd.Flags().StringArrayP("label", "l", nil, "Target commands matching labels")
	rmCmd.Flags().BoolP("force", "f", false, "Force remove running commands (sends SIGKILL)")
}

var rmCmd = &cobra.Command{
	Use:   "rm [flags] [ID|NAME...]",
	Short: "Remove a stopped command",
	RunE:  runRm,
}

func runRm(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	labelSlice, _ := cmd.Flags().GetStringArray("label")
	labels, err := parseLabels(labelSlice)
	if err != nil {
		return err
	}

	svc, err := cmdmanService()
	if err != nil {
		return err
	}

	results, err := svc.Remove(cmd.Context(), cmdman.RemoveRequest{
		Targets: args,
		Labels:  labels,
		Force:   force,
	})
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.Err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "rm %s: %v\n", result.ID, result.Err)
			continue
		}
		fmt.Fprintln(cmd.OutOrStdout(), result.ID)
	}
	return nil
}

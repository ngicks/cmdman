package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func rmCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagLabel []string
		flagForce bool
	)

	cmd := &cobra.Command{
		Use:   "rm [flags] [ID|NAME...]",
		Short: "Remove a stopped command",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRm(cmd, args, rootCfg, flagLabel, flagForce)
		},
	}

	cmd.Flags().StringArrayVarP(&flagLabel, "label", "l", nil, "Target commands matching labels")
	cmd.Flags().
		BoolVarP(&flagForce, "force", "f", false, "Force remove running commands (sends SIGKILL)")

	parent.AddCommand(cmd)
}

func runRm(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	labelSlice []string,
	force bool,
) error {
	labels, err := parseLabels(labelSlice)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
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

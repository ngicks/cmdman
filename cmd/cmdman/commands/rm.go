package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
)

func rmCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		flagLabel        []string
		flagForce        bool
		flagIgnoreErrors bool
	)

	cmd := &cobra.Command{
		Use:               "rm [flags] [ID|NAME...]",
		Short:             "Remove a stopped command",
		ValidArgsFunction: completeCommandNames(rf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRm(cmd, args, rf, flagLabel, flagForce, flagIgnoreErrors)
		},
	}

	cmd.Flags().StringArrayVarP(&flagLabel, "label", "l", nil, "Target commands matching labels")
	cmd.Flags().
		BoolVarP(&flagForce, "force", "f", false, "Force remove running commands (sends SIGKILL)")
	cmd.Flags().BoolVar(&flagIgnoreErrors, "ignore-errors", false,
		"Exit 0 even when some targets failed; failures are still printed")

	parent.AddCommand(cmd)
}

func runRm(
	cmd *cobra.Command,
	args []string,
	rf *rootFlags,
	labelSlice []string,
	force bool,
	ignoreErrors bool,
) error {
	labels, err := parseLabels(labelSlice)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	results, err := svc.Remove(cmd.Context(), cmdman.RemoveRequest{
		Targets: args,
		Labels:  labels,
		Force:   force,
	})
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.Err == nil {
			fmt.Fprintln(cmd.OutOrStdout(), result.ID)
		}
	}
	return reportTargetErrors(cmd.ErrOrStderr(), "rm", ignoreErrors,
		func(yield func(string, error) bool) {
			for _, result := range results {
				if !yield(result.ID, result.Err) {
					return
				}
			}
		})
}

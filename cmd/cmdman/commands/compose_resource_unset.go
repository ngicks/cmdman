package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeResourceUnsetCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagScale int
	)

	cmd := &cobra.Command{
		Use:   "unset [--scale N] COMMAND KEY",
		Short: "Remove the value of a resource",
		Long: `Remove the value of a resource without running its release event.

Removing a resource that has no value is not an error.`,
		Args:              resourceArgs(cobra.ExactArgs(2), &flagScale),
		ValidArgsFunction: completeFirstComposeCommand(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeResourceUnset(cmd, rf, cf, args, flagScale)
		},
	}

	addResourceScaleFlag(cmd, &flagScale)

	parent.AddCommand(cmd)
}

func runComposeResourceUnset(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	args []string,
	scale int,
) error {
	selection, opt, err := composeResourceTarget(cmd, cf, args[0], args[1], scale)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	return compose.NewService(svc).ResourceUnset(cmd.Context(), selection, opt)
}

package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeResourceSetCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagScale int
	)

	cmd := &cobra.Command{
		Use:   "set [--scale N] COMMAND KEY VALUE",
		Short: "Store the value of a resource",
		Long: `Store the value of a resource.

A value already stored is replaced, and the release event recorded with it is
kept. A value stored here for the first time has no release event.`,
		Args:              resourceArgs(cobra.ExactArgs(3), &flagScale),
		ValidArgsFunction: completeFirstComposeCommand(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeResourceSet(cmd, rf, cf, args, flagScale)
		},
	}

	addResourceScaleFlag(cmd, &flagScale)

	parent.AddCommand(cmd)
}

func runComposeResourceSet(
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

	return compose.NewService(svc).ResourceSet(cmd.Context(), selection, opt, args[2])
}

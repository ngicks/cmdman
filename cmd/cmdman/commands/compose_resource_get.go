package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeResourceGetCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagScale int
	)

	cmd := &cobra.Command{
		Use:   "get [--scale N] COMMAND KEY",
		Short: "Print the value of a resource",
		Long: `Print the value of a resource.

Fails when no value is stored for the resource.`,
		Args:              resourceArgs(cobra.ExactArgs(2), &flagScale),
		ValidArgsFunction: completeFirstComposeCommand(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeResourceGet(cmd, rf, cf, args, flagScale)
		},
	}

	addResourceScaleFlag(cmd, &flagScale)

	parent.AddCommand(cmd)
}

func runComposeResourceGet(
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

	value, err := compose.NewService(svc).ResourceGet(cmd.Context(), selection, opt)
	if err != nil {
		return err
	}
	return cli.PrintResourceValue(cmd.OutOrStdout(), value)
}

package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// addResourceScaleFlag registers the --scale flag of the compose resource
// verbs, bound to scale.
func addResourceScaleFlag(cmd *cobra.Command, scale *int) {
	cmd.Flags().IntVar(scale, scaleFlagName, 0,
		"Scale index (1-based) of the replica; defaults to the caller's own replica,"+
			" or to the sole one")
}

// resourceArgs wraps validate so that a --scale given explicitly is at least 1.
func resourceArgs(validate cobra.PositionalArgs, scale *int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return err
		}
		if cmd.Flags().Changed(scaleFlagName) && *scale < 1 {
			return fmt.Errorf("--%s must be 1 or greater, got %d", scaleFlagName, *scale)
		}
		return nil
	}
}

// composeResourceTarget resolves the project and the resource a compose
// resource verb addresses. Without --scale the scale index comes from the
// environment of a replica or hook of the same command, if that is where the
// verb runs.
func composeResourceTarget(
	cmd *cobra.Command,
	cf *composeFlags,
	command, key string,
	scale int,
) (compose.ProjectSelection, compose.ResourceOption, error) {
	selection, err := compose.ResolveContextSelection(cf.normalizeOpts(), os.LookupEnv)
	if err != nil {
		return compose.ProjectSelection{}, compose.ResourceOption{}, err
	}
	if !cmd.Flags().Changed(scaleFlagName) {
		scale = compose.ContextScaleIndex(selection, command, os.LookupEnv)
	}
	return selection, compose.ResourceOption{Command: command, ScaleIndex: scale, Key: key}, nil
}

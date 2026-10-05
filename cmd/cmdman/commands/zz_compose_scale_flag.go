package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// scaleFlagName is the --scale flag shared by the compose verbs that can act on
// one replica of one command. attach and capture-screen keep a --scale of their
// own, where 0 picks the sole replica.
const scaleFlagName = "scale"

// addScaleFlag registers --scale on cmd, bound to scale.
func addScaleFlag(cmd *cobra.Command, scale *int) {
	cmd.Flags().IntVar(scale, scaleFlagName, 0,
		"Act on only the replica with this scale index (1-based); takes exactly one COMMAND")
}

// scaleArgs wraps validate so that, when --scale is given, the command takes
// exactly one COMMAND and the index is at least 1.
func scaleArgs(validate cobra.PositionalArgs, scale *int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return err
		}
		return checkScaleArgs(cmd, args, *scale)
	}
}

// scaleArgsBeforeDash is scaleArgs for a command whose COMMAND arguments end at
// `--`. Without `--` the arguments cannot be split, so the check is left to the
// run function, which reports the missing separator.
func scaleArgsBeforeDash(validate cobra.PositionalArgs, scale *int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return err
		}
		dash := cmd.ArgsLenAtDash()
		if dash < 0 {
			return nil
		}
		return checkScaleArgs(cmd, args[:dash], *scale)
	}
}

func checkScaleArgs(cmd *cobra.Command, names []string, scale int) error {
	if !cmd.Flags().Changed(scaleFlagName) {
		return nil
	}
	if scale < 1 {
		return fmt.Errorf("--%s must be 1 or greater, got %d", scaleFlagName, scale)
	}
	switch len(names) {
	case 1:
		return nil
	case 0:
		return fmt.Errorf("--%s needs exactly one COMMAND, got none", scaleFlagName)
	default:
		return fmt.Errorf(
			"--%s needs exactly one COMMAND, got %d: %s",
			scaleFlagName, len(names), strings.Join(names, " "))
	}
}

// composeTargets converts COMMAND arguments into compose targets. Each name
// selects every replica of its command, or only replica scale when scale > 0.
func composeTargets(names []string, scale int) []compose.Target {
	targets := compose.TargetsOf(names...)
	for i := range targets {
		targets[i].ScaleIndex = scale
	}
	return targets
}

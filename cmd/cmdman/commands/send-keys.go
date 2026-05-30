package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func sendKeysCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagLiteral     bool
		flagHex         bool
		flagRepeatCount int
	)

	cmd := &cobra.Command{
		Use:     "send-keys [flags] ID|NAME KEY [KEY...]",
		Aliases: []string{"send"},
		Short:   "Send key input to a running command PTY",
		Args:    cobra.MinimumNArgs(2),
		// Only the first positional is a command target; the rest are key names.
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeCommandNames(rootCfg, runningStates...)(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSendKeys(cmd, args, rootCfg, flagLiteral, flagHex, flagRepeatCount)
		},
	}

	flags := cmd.Flags()
	flags.BoolVarP(&flagLiteral, "literal", "l", false,
		"Send keys literally, without translating key names")
	flags.BoolVarP(&flagHex, "hex", "H", false, "Treat keys as hexadecimal byte values")
	flags.IntVarP(&flagRepeatCount, "repeat-count", "N", 1, "Repeat the key sequence N times")

	parent.AddCommand(cmd)
}

func runSendKeys(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	literal, hexMode bool,
	repeatCount int,
) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	return svc.SendKeys(cmd.Context(), args[0], cmdman.SendKeysRequest{
		Keys:        args[1:],
		Literal:     literal,
		Hex:         hexMode,
		RepeatCount: repeatCount,
	})
}

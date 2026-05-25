package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeSendKeysCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagLiteral     bool
		flagHex         bool
		flagRepeatCount int
	)

	cmd := &cobra.Command{
		Use:     "send-keys [COMMAND...] -- KEY [KEY...]",
		Aliases: []string{"send"},
		Short:   "Send key input to compose command PTYs",
		Long: "Send key input to the PTYs of compose commands.\n\n" +
			"Command names and keys are separated by `--`: everything before `--`\n" +
			"selects compose commands (every command when empty), everything after\n" +
			"is the key sequence sent to each. Examples:\n" +
			"  cmdman compose send-keys api worker -- C-c Enter\n" +
			"  cmdman compose send-keys -- Enter   # broadcast to every command",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSendKeys(cmd, rootCfg, cf, args, flagLiteral, flagHex, flagRepeatCount)
		},
	}

	flags := cmd.Flags()
	flags.BoolVarP(&flagLiteral, "literal", "l", false,
		"Send keys literally, without translating key names")
	flags.BoolVarP(&flagHex, "hex", "H", false, "Treat keys as hexadecimal byte values")
	flags.IntVarP(&flagRepeatCount, "repeat-count", "N", 1, "Repeat the key sequence N times")

	parent.AddCommand(cmd)
}

func runComposeSendKeys(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	args []string,
	literal, hexMode bool,
	repeatCount int,
) error {
	dash := cmd.ArgsLenAtDash()
	if dash < 0 {
		return fmt.Errorf(
			"missing `--` separator; usage: compose send-keys [COMMAND...] -- KEY [KEY...]")
	}
	commandNames := args[:dash]
	keys := args[dash:]
	if len(keys) == 0 {
		return fmt.Errorf("no keys provided after `--`")
	}

	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	result, err := compose.NewService(svc).SendKeys(cmd.Context(), selection, compose.SendKeysOption{
		CommandNames: commandNames,
		Keys:         keys,
		Literal:      literal,
		Hex:          hexMode,
		RepeatCount:  repeatCount,
	})
	if err != nil {
		return err
	}

	return cli.PrintSendKeysResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Outcomes)
}

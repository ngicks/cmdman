package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeSignalCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagSignal string
	)

	cmd := &cobra.Command{
		Use:               "signal [COMMAND...]",
		Short:             "Send a signal to compose commands",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rootCfg, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSignal(cmd, rootCfg, cf, args, flagSignal)
		},
	}

	cmd.Flags().StringVarP(
		&flagSignal, "signal", "s", "",
		"Signal to send (e.g. SIGTERM, HUP, 15); required",
	)
	_ = cmd.RegisterFlagCompletionFunc("signal", signalCompletions)

	parent.AddCommand(cmd)
}

func runComposeSignal(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	signal string,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	result, err := compose.NewService(svc).Signal(cmd.Context(), selection, compose.SignalOption{
		CommandNames: commandNames,
		Signal:       signal,
	})
	if err != nil {
		return err
	}

	return cli.PrintSignalResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Outcomes)
}

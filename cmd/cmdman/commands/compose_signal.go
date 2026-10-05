package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeSignalCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagSignal string
		flagScale  int
	)

	cmd := &cobra.Command{
		Use:               "signal [COMMAND...]",
		Short:             "Send a signal to compose commands",
		Args:              scaleArgs(cobra.ArbitraryArgs, &flagScale),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeSignal(cmd, rf, cf, args, flagScale, flagSignal)
		},
	}

	cmd.Flags().StringVarP(
		&flagSignal, "signal", "s", "",
		"Signal to send (e.g. SIGTERM, HUP, 15); required",
	)
	addScaleFlag(cmd, &flagScale)
	_ = cmd.RegisterFlagCompletionFunc("signal", signalCompletions)

	parent.AddCommand(cmd)
}

func runComposeSignal(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	scale int,
	signal string,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	result, err := compose.NewService(svc).Signal(cmd.Context(), selection, compose.SignalOption{
		Targets: composeTargets(commandNames, scale),
		Signal:  signal,
	})
	if err != nil {
		return err
	}

	return cli.PrintSignalResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Outcomes)
}

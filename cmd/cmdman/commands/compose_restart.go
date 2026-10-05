package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeRestartCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagProgress string
		flagScale    int
	)

	cmd := &cobra.Command{
		Use:               "restart [COMMAND...]",
		Short:             "Stop then start compose commands",
		Args:              scaleArgs(cobra.ArbitraryArgs, &flagScale),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeRestart(cmd, rf, cf, args, flagScale, flagProgress)
		},
	}

	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)
	addScaleFlag(cmd, &flagScale)
	_ = cmd.RegisterFlagCompletionFunc("progress", progressCompletions)

	parent.AddCommand(cmd)
}

func runComposeRestart(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	scale int,
	progress string,
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

	prog, err := resolveComposeProgress(cmd, progress, "restart")
	if err != nil {
		return err
	}
	defer prog.Close()

	result, err := compose.NewService(svc, compose.WithReporter(prog)).Restart(
		cmd.Context(), selection, compose.RestartOption{
			Targets: composeTargets(commandNames, scale),
		})
	if err != nil {
		return err
	}

	// The tty progress renderer repaints its block in place with cursor-up
	// sequences from a background ticker; finalize it before the result lines
	// reach the same terminal. Close is idempotent, so the deferred one still runs.
	_ = prog.Close()
	return cli.PrintRestartResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Restarts)
}

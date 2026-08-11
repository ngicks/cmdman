package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeStopCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagProgress string
	)

	cmd := &cobra.Command{
		Use:               "stop [COMMAND...]",
		Short:             "Stop running compose commands",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeStop(cmd, rf, cf, args, flagProgress)
		},
	}

	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)
	_ = cmd.RegisterFlagCompletionFunc("progress", progressCompletions)

	parent.AddCommand(cmd)
}

func runComposeStop(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
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

	prog, err := resolveComposeProgress(cmd, progress, "stop")
	if err != nil {
		return err
	}
	defer prog.Close()

	result, err := compose.NewService(svc, compose.WithReporter(prog)).Stop(
		cmd.Context(), selection, compose.StopOption{
			CommandNames: commandNames,
		})
	if err != nil {
		return err
	}

	return cli.StopResultErr(result.Stops)
}

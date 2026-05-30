package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeDownCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagProgress string
	)

	cmd := &cobra.Command{
		Use:               "down [COMMAND...]",
		Short:             "Stop and remove compose commands",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rootCfg, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeDown(cmd, rootCfg, cf, args, flagProgress)
		},
	}

	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)
	_ = cmd.RegisterFlagCompletionFunc("progress", progressCompletions)

	parent.AddCommand(cmd)
}

func runComposeDown(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	progress string,
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

	prog, err := resolveComposeProgress(cmd, progress, "down")
	if err != nil {
		return err
	}
	defer prog.Close()

	result, err := compose.NewService(svc, compose.WithReporter(prog)).Down(
		cmd.Context(), selection, compose.DownOption{
			CommandNames: commandNames,
		})
	if err != nil {
		return err
	}

	return cli.DownResultErr(result)
}

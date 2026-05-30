package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeStartCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagProgress string
	)

	cmd := &cobra.Command{
		Use:   "start [COMMAND...]",
		Short: "Start compose commands honoring after.Condition",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeStart(cmd, rootCfg, cf, args, flagProgress)
		},
	}

	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)

	parent.AddCommand(cmd)
}

func runComposeStart(
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

	prog, err := resolveComposeProgress(cmd, progress, "start")
	if err != nil {
		return err
	}
	defer prog.Close()

	result, err := compose.NewService(svc, compose.WithReporter(prog)).Start(
		cmd.Context(), selection, compose.StartOption{
			CommandNames: commandNames,
		})
	if err != nil {
		return err
	}

	return cli.StartResultErr(result.Starts)
}

package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeUpCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagRemoveOrphan bool
		flagProgress     string
	)

	cmd := &cobra.Command{
		Use:   "up [COMMAND...]",
		Short: "Create and start compose commands (detached)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeUp(cmd, rootCfg, cf, args, flagRemoveOrphan, flagProgress)
		},
	}

	cmd.Flags().BoolVar(&flagRemoveOrphan, "remove-orphan", false,
		"Remove stopped orphan commands (running orphans are skipped)")
	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)

	parent.AddCommand(cmd)
}

func runComposeUp(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	removeOrphan bool,
	progress string,
) error {
	spec, err := compose.LoadAndNormalize(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	prog, err := resolveComposeProgress(cmd, progress, "up")
	if err != nil {
		return err
	}
	defer prog.Close()

	result, err := compose.NewService(svc, compose.WithReporter(prog)).Up(
		cmd.Context(), spec, compose.UpOption{
			CreateOption: compose.CreateOption{
				RemoveOrphan: removeOrphan,
				CommandNames: commandNames,
			},
			StartOption: compose.StartOption{
				CommandNames: commandNames,
			},
		})
	if err != nil {
		return err
	}

	return cli.UpResultErr(result)
}

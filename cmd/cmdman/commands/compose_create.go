package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeCreateCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagRemoveOrphan bool
	)

	cmd := &cobra.Command{
		Use:   "create [COMMAND...]",
		Short: "Create compose commands without starting them",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeCreate(cmd, rootCfg, cf, args, flagRemoveOrphan)
		},
	}

	cmd.Flags().BoolVar(&flagRemoveOrphan, "remove-orphan", false,
		"Remove stopped orphan commands (running orphans are skipped)")

	parent.AddCommand(cmd)
}

func runComposeCreate(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	removeOrphan bool,
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

	result, err := compose.NewService(svc).Create(cmd.Context(), spec, compose.CreateOption{
		RemoveOrphan: removeOrphan,
		CommandNames: commandNames,
	})
	if err != nil {
		return err
	}

	return cli.PrintCreateResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result)
}

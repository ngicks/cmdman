package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeCreateCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagRemoveOrphan bool
	)

	cmd := &cobra.Command{
		Use:               "create [COMMAND...]",
		Short:             "Create compose commands without starting them",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeCreate(cmd, rf, cf, args, flagRemoveOrphan)
		},
	}

	cmd.Flags().BoolVar(&flagRemoveOrphan, "remove-orphan", false,
		"Remove stopped orphan commands (running orphans are skipped)")

	parent.AddCommand(cmd)
}

func runComposeCreate(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	removeOrphan bool,
) error {
	spec, err := compose.LoadAndNormalize(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
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

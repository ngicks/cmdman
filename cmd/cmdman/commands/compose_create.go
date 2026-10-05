package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeCreateCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagRemoveOrphan bool
		flagProgress     string
		flagScale        int
	)

	cmd := &cobra.Command{
		Use:               "create [COMMAND...]",
		Short:             "Create compose commands without starting them",
		Args:              scaleArgs(cobra.ArbitraryArgs, &flagScale),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeCreate(cmd, rf, cf, args, flagScale, flagRemoveOrphan, flagProgress)
		},
	}

	cmd.Flags().BoolVar(&flagRemoveOrphan, "remove-orphan", false,
		"Remove stopped orphan commands (running orphans are skipped)")
	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)
	addScaleFlag(cmd, &flagScale)
	_ = cmd.RegisterFlagCompletionFunc("progress", progressCompletions)

	parent.AddCommand(cmd)
}

func runComposeCreate(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	scale int,
	removeOrphan bool,
	progress string,
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

	prog, err := resolveComposeProgress(cmd, progress, "create")
	if err != nil {
		return err
	}
	defer prog.Close()

	result, err := compose.NewService(svc, compose.WithReporter(prog)).Create(
		cmd.Context(), spec, compose.CreateOption{
			RemoveOrphan: removeOrphan,
			Targets:      composeTargets(commandNames, scale),
		})
	if err != nil {
		return err
	}

	// The tty progress renderer repaints its block in place with cursor-up
	// sequences from a background ticker; finalize it before the result lines
	// reach the same terminal. Close is idempotent, so the deferred one still runs.
	_ = prog.Close()
	return cli.PrintCreateResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result)
}

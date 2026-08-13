package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeUpCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagRemoveOrphan bool
		flagProgress     string
		flagMux          bool
	)

	cmd := &cobra.Command{
		Use:   "up [COMMAND...]",
		Short: "Create and start compose commands (detached)",
		Long: `Create and start the compose project's commands, detached.

With --mux, a successful up is followed by the multiplexer dashboard described
by the compose file's "mux:" section, so one command both brings the project up
and shows its layout. The dashboard opens at layout 0 instead of cycling, so
re-running up --mux keeps the same layout; cycling stays with "compose mux up".
A project with no "mux:" section is brought up as usual and the dashboard is
skipped with a warning.`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeUp(cmd, rf, cf, args, flagRemoveOrphan, flagProgress, flagMux)
		},
	}

	cmd.Flags().BoolVar(&flagRemoveOrphan, "remove-orphan", false,
		"Remove stopped orphan commands (running orphans are skipped)")
	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)
	cmd.Flags().BoolVar(&flagMux, "mux", false,
		`Open the compose file's "mux:" dashboard after a successful up`+
			" (skipped with a warning when the file has no mux: section)")
	_ = cmd.RegisterFlagCompletionFunc("progress", progressCompletions)

	parent.AddCommand(cmd)
}

func runComposeUp(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	removeOrphan bool,
	progress string,
	withMux bool,
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

	prog, err := resolveComposeProgress(cmd, progress, "up")
	if err != nil {
		return err
	}
	defer prog.Close()

	// The frame seam is set because of the --mux tail below: its MuxUp shows the
	// configured default_frame, which may hold a managed entry.
	composeSvc := compose.NewService(
		svc,
		compose.WithReporter(prog),
		compose.WithFrameSvc(cli.NewFrameSvc(svc)),
	)
	result, err := composeSvc.Up(
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
	if err := cli.UpResultErr(result); err != nil {
		return err
	}
	if !withMux {
		return nil
	}

	// The tty progress renderer repaints its block in place with cursor-up
	// sequences from a background ticker; finalize it before the mux tail writes
	// to the same terminal. Close is idempotent, so the deferred one still runs.
	_ = prog.Close()
	return cli.ComposeUpMux(
		cmd.Context(),
		composeSvc,
		&spec,
		cmd.OutOrStdout(),
		cmd.ErrOrStderr(),
	)
}

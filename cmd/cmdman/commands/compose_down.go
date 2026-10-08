package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeDownCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagProgress string
		flagForce    bool
		flagTimeout  string
	)

	cmd := &cobra.Command{
		Use:   "down [COMMAND...]",
		Short: "Stop and remove compose commands",
		Long: `Stop and remove compose commands.

With no COMMAND, down tears the whole project down; with COMMANDs, it removes
those commands and the commands that depend on them. Down removes every replica
of a command; to remove replicas, scale the command down with
"cmdman compose scale COMMAND=N".`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeDown(cmd, rf, cf, args, flagProgress, flagForce, flagTimeout)
		},
	}

	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)
	_ = cmd.RegisterFlagCompletionFunc("progress", progressCompletions)
	// No -f shorthand: the compose group's persistent --file owns -f.
	cmd.Flags().BoolVar(&flagForce, "force", false,
		"Treat every hook that fails under on_error fail as on_error continue, and tear"+
			" down a replica with undecodable stored hooks without running them")
	addStopTimeoutFlag(cmd, &flagTimeout)

	parent.AddCommand(cmd)
}

func runComposeDown(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	progress string,
	force bool,
	timeoutValue string,
) error {
	timeout, err := stopTimeoutFlag(cmd, timeoutValue)
	if err != nil {
		return err
	}

	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
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
			Targets: compose.TargetsOf(commandNames...),
			Force:   force,
			Timeout: timeout,
		})
	if err != nil {
		return err
	}

	return cli.DownResultErr(result)
}

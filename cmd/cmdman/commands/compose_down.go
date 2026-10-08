package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeDownCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagProgress     string
		flagForce        bool
		flagTimeout      string
		flagCloseWindows bool
	)

	cmd := &cobra.Command{
		Use:   "down [COMMAND...]",
		Short: "Stop and remove compose commands",
		Long: `Stop and remove compose commands.

With no COMMAND, down tears the whole project down; with COMMANDs, it removes
those commands and the commands that depend on them. Down removes every replica
of a command; to remove replicas, scale the command down with
"cmdman compose scale COMMAND=N".

With --close-windows, a whole-project down that removed every replica also
closes the project's multiplexer windows. Two windows are restored instead: the
window of the pane that runs this command, and a window cmdman took over from
the user.`,
		Args:              closeWindowsArgs(cobra.ArbitraryArgs, &flagCloseWindows),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeDown(
				cmd, rf, cf, args, flagProgress, flagForce, flagTimeout, flagCloseWindows,
			)
		},
	}

	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)
	_ = cmd.RegisterFlagCompletionFunc("progress", progressCompletions)
	// No -f shorthand: the compose group's persistent --file owns -f.
	cmd.Flags().BoolVar(&flagForce, "force", false,
		"Treat every hook that fails under on_error fail as on_error continue, and tear"+
			" down a replica with undecodable stored hooks without running them")
	addStopTimeoutFlag(cmd, &flagTimeout)
	cmd.Flags().BoolVar(&flagCloseWindows, "close-windows", false,
		"Close the project's multiplexer windows after a down that removed every replica"+
			" (whole-project down only)")

	parent.AddCommand(cmd)
}

// closeWindowsArgs wraps validate so that --close-windows refuses COMMAND
// arguments. A targeted down leaves the project's other commands running, and
// the windows viewing them are still in use.
func closeWindowsArgs(validate cobra.PositionalArgs, closeWindows *bool) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return err
		}
		if *closeWindows && len(args) > 0 {
			return fmt.Errorf(
				"--close-windows needs a whole-project down and takes no COMMAND, got %q", args,
			)
		}
		return nil
	}
}

func runComposeDown(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	progress string,
	force bool,
	timeoutValue string,
	closeWindows bool,
) error {
	timeout, err := stopTimeoutFlag(cmd, timeoutValue)
	if err != nil {
		return err
	}
	parallel, err := composeParallelOption(cmd, cf)
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

	result, err := compose.NewService(svc, compose.WithReporter(prog), parallel).Down(
		cmd.Context(), selection, compose.DownOption{
			Targets: compose.TargetsOf(commandNames...),
			Force:   force,
			Timeout: timeout,
		})
	if err != nil {
		return err
	}
	if err := cli.DownResultErr(result); err != nil {
		return err
	}
	if !closeWindows {
		return nil
	}

	// The tty progress renderer repaints its block in place with cursor-up
	// sequences from a background ticker; finalize it before a warning reaches the
	// same terminal. Close is idempotent, so the deferred one still runs.
	_ = prog.Close()
	cli.CloseProjectWindows(cmd.Context(), selection, cmd.ErrOrStderr())
	return nil
}

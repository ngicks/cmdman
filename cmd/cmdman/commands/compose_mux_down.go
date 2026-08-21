package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeMuxDownCmd(
	parent *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	parentSession *string,
) {
	var flagSession string

	cmd := &cobra.Command{
		Use:               "down",
		Short:             "Tear down the dashboard windows for this compose project",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: `Tear down the cmdman-owned dashboard windows matching this compose project.

The in-pane viewers are detached, the window collapses to a single clean pane,
and the tmux options cmdman set are cleared. The supervised commands keep
running — only the disposable viewers are torn down.

Window discovery is server-wide and requires no $TMUX context; it works from
any pane, run-shell, or outside tmux. --session narrows the scan to one session.

Down resolves no leaves and touches none of the project's commands — only the
project identity derived from the compose file is required.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runComposeMuxDown(cmd, rf, cf, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow teardown to this tmux session only (default: server-wide)",
	)

	parent.AddCommand(cmd)
}

// runComposeMuxDown tears down the compose project's dashboard. The teardown
// itself needs no leaf resolution — only the project identity and spec driver
// options are required — but registering the worker that carries it out is an
// ordinary command creation, so the service is opened here. The layout argument
// is irrelevant to teardown.
func runComposeMuxDown(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	session string,
) error {
	logName, err := composeMuxLogName(cf)
	if err != nil {
		return err
	}
	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	opts := muxOpOptions(cmd, svc, logName)
	return cli.RunMuxOp(cmd.Context(), opts, func(ctx context.Context) error {
		return composeMuxDownOp(ctx, cmd, cf, session)
	})
}

// composeMuxDownOp is everything `compose mux down` does, split out so
// [cli.RunMuxOp] decides where it runs: here, or in a worker that outlives the
// invoking pane.
func composeMuxDownOp(
	ctx context.Context,
	cmd *cobra.Command,
	cf *composeFlags,
	session string,
) error {
	selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
	if err != nil {
		return err
	}

	// The teardown itself reaches none of the project's commands — the project
	// identity derived from the compose file is all it works from (see this
	// command's Long help) — so the compose service is built without one.
	return compose.NewService(nil).MuxDown(ctx, compose.MuxDownOption{
		Selection:   selection,
		SessionName: session,
		Stdout:      cmd.OutOrStdout(),
	})
}

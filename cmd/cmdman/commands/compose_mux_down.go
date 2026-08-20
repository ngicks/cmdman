package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeMuxDownCmd(parent *cobra.Command, cf *composeFlags, parentSession *string) {
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

Down needs no cmdman service or leaf resolution — only the project identity
derived from the compose file is required.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runComposeMuxDown(cmd, cf, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow teardown to this tmux session only (default: server-wide)",
	)

	parent.AddCommand(cmd)
}

// runComposeMuxDown tears down the compose project's dashboard. It needs no
// cmdman service or leaf resolution — only the project identity and spec driver
// options are required. The layout argument is irrelevant to teardown.
func runComposeMuxDown(cmd *cobra.Command, cf *composeFlags, session string) error {
	return cli.RunMuxOp(cmd.Context(), cli.MuxOpOptions{}, func(ctx context.Context) error {
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

	// Down needs no cmdman service — only the project identity derived from the
	// compose file (see this command's Long help). Pass a nil service.
	return compose.NewService(nil).MuxDown(ctx, compose.MuxDownOption{
		Selection:   selection,
		SessionName: session,
		Stdout:      cmd.OutOrStdout(),
	})
}

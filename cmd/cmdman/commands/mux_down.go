package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/mux"
)

func muxDownCmd(parent *cobra.Command, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "down [path]",
		Short: "Tear down the dashboard windows for this spec",
		Long: `Tear down the cmdman-owned dashboard windows matching this spec's identity.

The in-pane viewers are detached, the window collapses to a single clean pane,
and the tmux options cmdman set are cleared. The supervised commands keep
running — only the disposable viewers are torn down.

A layout file path is optional: it is only read to extract the driver
configuration (e.g. a custom socket). With no path (or the stdin default "-"),
teardown uses the default driver with no custom options.

Window discovery is server-wide and requires no $TMUX context; it works from
any pane, run-shell, or outside tmux. --session narrows the scan to one session.`,
		Args: cobra.MaximumNArgs(1),
		// The positional arg is an optional layout file; file completion is appropriate.
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxDown(cmd, args, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow teardown to this tmux session only (default: server-wide)",
	)

	parent.AddCommand(cmd)
}

// runMuxDown tears the dashboard down instead of building it. The spec path is
// optional: it is only read when an explicit path is given, to extract the
// driver configuration. With the stdin default ("-") teardown uses the default
// driver rather than blocking on stdin.
func runMuxDown(cmd *cobra.Command, args []string, session string) error {
	return cli.RunMuxOp(cmd.Context(), cli.MuxOpOptions{}, func(ctx context.Context) error {
		return muxDownOp(ctx, cmd, args, session)
	})
}

// muxDownOp is everything `mux down` does, split out so [cli.RunMuxOp] decides
// where it runs: here, or in a worker that outlives the invoking pane.
func muxDownOp(
	ctx context.Context,
	cmd *cobra.Command,
	args []string,
	session string,
) error {
	path := "-"
	if len(args) == 1 {
		path = args[0]
	}

	driver, err := specDriver(path)
	if err != nil {
		return err
	}

	return mux.Down(ctx, mux.DownOptions{
		Driver:      driver,
		SessionName: session,
		Stdout:      cmd.OutOrStdout(),
	})
}

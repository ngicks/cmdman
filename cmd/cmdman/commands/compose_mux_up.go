package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeMuxUpCmd(
	parent *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	parentSession *string,
) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "up [layout]",
		Short: "Open a multiplexer dashboard for a compose project",
		Long: `Open a multiplexer dashboard described by the compose file's "mux:" section.

Each leaf references a compose service name; panes run cmdman attach by default,
or cmdman logs when mode: logs.

With no argument, mux cycles to the next layout each invocation (a fresh window
starts at the first layout). Pass a layout name or a 0-based index to apply that
layout directly; the choice becomes the new cycle position. A name is matched
before an index, so a layout literally named "2" wins over index 2.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".

The compose file must contain a top-level "mux:" section; a missing section
is an error (no synthesized default).`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeComposeMuxLayout(cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runComposeMuxUp(cmd, rf, cf, args, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)

	parent.AddCommand(cmd)
}

func runComposeMuxUp(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	args []string,
	session string,
) error {
	return cli.RunMuxOp(cmd.Context(), cli.MuxOpOptions{}, func(ctx context.Context) error {
		return composeMuxUpOp(ctx, cmd, rf, cf, args, session)
	})
}

// composeMuxUpOp is everything `compose mux up` does, split out so
// [cli.RunMuxOp] decides where it runs: here, or in a worker that outlives the
// invoking pane.
func composeMuxUpOp(
	ctx context.Context,
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	args []string,
	session string,
) error {
	selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	var layout string
	if len(args) > 0 {
		layout = args[0]
	}
	return compose.NewService(svc, compose.WithFrameSvc(cli.NewFrameSvc(svc))).MuxUp(
		ctx,
		compose.MuxUpOption{
			Selection:   selection,
			Layout:      layout,
			SessionName: session,
			Stdout:      cmd.OutOrStdout(),
		})
}

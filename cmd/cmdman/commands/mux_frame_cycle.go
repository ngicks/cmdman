package commands

import (
	"context"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/spf13/cobra"
)

func muxFrameCycleCmd(parent *cobra.Command, rf *rootFlags, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "cycle",
		Short: "Show the next frame def around the current window",
		Long: `Show the next frame def around the current window.

The rotation is the sorted list of defs discoverable under the frame config
dir. It starts after the def currently shown and wraps around; an unframed
window - or one showing a def that is not among them, e.g. shown by path or
deleted since - gets the first def.

Each step replaces the frame in place, exactly as "frame show DEF" does.`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxFrameCycle(cmd, rf, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Cycle the frame of this tmux session's current window (default: the window you are in)",
	)

	parent.AddCommand(cmd)
}

func runMuxFrameCycle(cmd *cobra.Command, rf *rootFlags, session string) error {
	return cli.RunMuxOp(cmd.Context(), func(ctx context.Context) error {
		return muxFrameCycleOp(ctx, cmd, rf, session)
	})
}

// muxFrameCycleOp is everything `mux frame cycle` does, split out so
// [cli.RunMuxOp] decides where it runs: here, or in a worker that outlives the
// invoking pane.
func muxFrameCycleOp(
	ctx context.Context,
	cmd *cobra.Command,
	rf *rootFlags,
	session string,
) error {
	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	return mux.FrameCycle(ctx, mux.FrameOptions{
		Session: session,
		Config:  svc.Config(),
		Svc:     cli.NewFrameSvc(svc),
	})
}

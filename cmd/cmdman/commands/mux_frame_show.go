package commands

import (
	"context"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/spf13/cobra"
)

func muxFrameShowCmd(parent *cobra.Command, rf *rootFlags, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "show [DEF]",
		Short: "Show a frame around the current window",
		Long: `Show the frame def DEF around the current window.

DEF is a bare name resolved under the frame config dir (<name>.yaml/.yml) or a
path to a def file. With no DEF the config key default_frame is used; with
neither, the error lists the defs found on disk.

Showing the def already up is a no-op; showing a different one replaces it in
place, which is how a frame is selected. Entries marked "managed: true" run as
supervised cmdman commands named frame-<def>-<i>: an already-running one is
adopted rather than started twice, and it keeps running once the frame is
taken down.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeFrameDefs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxFrameShow(cmd, rf, args, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Frame the current window of this tmux session (default: the window you are in)",
	)

	parent.AddCommand(cmd)
}

func runMuxFrameShow(
	cmd *cobra.Command,
	rf *rootFlags,
	args []string,
	session string,
) error {
	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	opts, err := muxFrameOpOptions(cmd, svc, session)
	if err != nil {
		return err
	}
	return cli.RunMuxOp(cmd.Context(), opts, func(ctx context.Context) error {
		return muxFrameShowOp(ctx, cmd, rf, args, session)
	})
}

// muxFrameShowOp is everything `mux frame show` does, split out so
// [cli.RunMuxOp] decides where it runs: here, or in a worker that outlives the
// invoking pane.
func muxFrameShowOp(
	ctx context.Context,
	cmd *cobra.Command,
	rf *rootFlags,
	args []string,
	session string,
) error {
	var def string
	if len(args) == 1 {
		def = args[0]
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	return mux.FrameShow(ctx, mux.FrameOptions{
		Def:     def,
		Session: session,
		Config:  svc.Config(),
		Svc:     cli.NewFrameSvc(svc),
	})
}

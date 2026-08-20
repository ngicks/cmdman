package commands

import (
	"context"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/spf13/cobra"
)

func muxFrameHideCmd(parent *cobra.Command, rf *rootFlags, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "hide",
		Short: "Take the frame around the current window down",
		Long: `Take the frame around the current window down.

The project region expands into the space it occupied. What comes down is the
frame panes themselves, not whatever def the window recorded, so a window left
holding frame panes under no def is cleaned up rather than skipped. A window
holding no frame pane is a quiet no-op.

The panes and their disposable viewers go away; a managed entry's supervised
command keeps running, and showing the frame again attaches to it instead of
starting a second one.`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxFrameHide(cmd, rf, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Unframe the current window of this tmux session (default: the window you are in)",
	)

	parent.AddCommand(cmd)
}

func runMuxFrameHide(cmd *cobra.Command, rf *rootFlags, session string) error {
	// Hiding a frame needs no cmdman service of its own — a managed entry's
	// command keeps running — but registering the worker that does the hiding is
	// an ordinary command creation, and that goes through the service.
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
		return mux.FrameHide(ctx, mux.FrameOptions{Session: session})
	})
}

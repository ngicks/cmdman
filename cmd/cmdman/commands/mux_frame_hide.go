package commands

import (
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/spf13/cobra"
)

func muxFrameHideCmd(parent *cobra.Command, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "hide",
		Short: "Take the frame around the current window down",
		Long: `Take the frame around the current window down.

The project region expands into the space it occupied. A window carrying no
frame is a quiet no-op.

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
			return runMuxFrameHide(cmd, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Unframe the current window of this tmux session (default: the window you are in)",
	)

	parent.AddCommand(cmd)
}

func runMuxFrameHide(cmd *cobra.Command, session string) error {
	return mux.FrameHide(cmd.Context(), mux.FrameOptions{Session: session})
}

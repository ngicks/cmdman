package commands

import (
	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/spf13/cobra"
)

func muxFrameLsCmd(parent *cobra.Command, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the frame defs and the windows they are shown around",
		Long: `List the frame defs discoverable on disk and the windows they are shown around.

Discovery is server-wide and requires no $TMUX context; it works from any pane,
from run-shell, or outside tmux. --session narrows the window scan to one
session; the def list is always the full one.

Columns: DEF, WINDOWS. WINDOWS holds the ids of the windows the def is shown
around, or "-" when it is shown nowhere. A def shown around a window without
being discoverable on disk - shown by path, or deleted since - still gets a
row, so nothing standing on a window is missing from the listing.`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxFrameLs(cmd, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow the window scan to this tmux session only (default: server-wide)",
	)

	parent.AddCommand(cmd)
}

func runMuxFrameLs(cmd *cobra.Command, session string) error {
	res, err := mux.FrameList(cmd.Context(), mux.FrameOptions{Session: session})
	if err != nil {
		return err
	}
	return cli.RenderFrameList(cmd.OutOrStdout(), res)
}

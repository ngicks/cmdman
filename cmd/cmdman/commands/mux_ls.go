package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

func muxLsCmd(parent *cobra.Command, parentSession *string) {
	var (
		flagSession string
		flagFormat  string
	)

	cmd := &cobra.Command{
		Use:   "ls [path]",
		Short: "List all cmdman-owned dashboard windows",
		Args:  cobra.MaximumNArgs(1),
		// The positional arg is an optional layout file path; file completion is appropriate.
		Long: `List all cmdman-owned dashboard windows on the server.

Discovery is server-wide and requires no $TMUX context; it works from any
pane, run-shell, or outside tmux. --session narrows the listing to one session.

A layout file path is optional: when given it is read only to extract the
driver configuration (for example a custom socket). With no path or the stdin
default "-", listing uses the default driver with no custom options.

Columns: SESSION, WINDOW, ID, IDENTITY, LAYOUT (-1 displayed as "-"), SCALE.
The SCALE column shows the replica positions stored on the window ("cmd=pos",
"-" when none are stored). Standalone mux ls has no replica counter; use
"compose mux ls" to see live counts ("cmd=pos/count").`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxLs(cmd, args, sess, flagFormat)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow listing to this tmux session only (default: server-wide)",
	)
	cmd.Flags().StringVar(&flagFormat, "format", "", cli.MuxLsFormatUsage())

	parent.AddCommand(cmd)
}

func runMuxLs(cmd *cobra.Command, args []string, session, format string) error {
	path := "-"
	if len(args) == 1 {
		path = args[0]
	}

	driver, err := specDriver(path)
	if err != nil {
		return err
	}

	windows, err := mux.List(cmd.Context(), mux.ListOptions{
		Driver:      driver,
		SessionName: session,
	})
	if err != nil {
		return err
	}
	// Standalone mux ls has no spec and no replica counter; cycle targets and
	// counts are unavailable. Pass nil for both so the SCALE column renders
	// stored positions only (or "-" when nothing is stored).
	return cli.RenderMuxWindows(cmd.OutOrStdout(), windows, nil, nil, format)
}

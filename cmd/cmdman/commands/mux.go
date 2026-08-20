package commands

import (
	"github.com/spf13/cobra"
)

func muxCmd(parent *cobra.Command, rf *rootFlags) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "mux [path]",
		Short: "Open a multiplexer dashboard for cmdman commands",
		Long: `Open a multiplexer dashboard described by a layout file (alias of "mux up").

Each leaf references a cmdman command (by ID or NAME); panes run cmdman attach
by default, or cmdman logs when mode: logs.

The layout file is a YAML document with a top-level mux: section. With no path
argument (or "-"), the spec is read from stdin.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".

Subcommands: up, down, ls, frame. A path literally named "up", "down", "ls",
or "frame" must be passed as: cmdman mux up <path>.`,
		Args: cobra.MaximumNArgs(1),
		// The positional arg is a layout file path; the shell's default file
		// completion is the right behavior, so ValidArgsFunction is left unset.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMuxUp(cmd, rf, args, flagSession)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)

	muxUpCmd(cmd, rf, &flagSession)
	muxDownCmd(cmd, rf, &flagSession)
	muxLsCmd(cmd, &flagSession)
	muxFrameCmd(cmd, rf, &flagSession)

	parent.AddCommand(cmd)
}

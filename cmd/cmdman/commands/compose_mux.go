package commands

import (
	"github.com/spf13/cobra"
)

func composeMuxCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "mux [layout]",
		Short: "Open a multiplexer dashboard for a compose project",
		Long: `Open a multiplexer dashboard described by the compose file's "mux:" section
(alias of "compose mux up").

Each leaf references a compose service name; panes run cmdman attach by default,
or cmdman logs when mode: logs.

With no argument, mux cycles to the next layout each invocation (a fresh window
starts at the first layout). Pass a layout name or a 0-based index to apply that
layout directly; the choice becomes the new cycle position. A name is matched
before an index, so a layout literally named "2" wins over index 2.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".

The compose file must contain a top-level "mux:" section; a missing section
is an error (no synthesized default).

Subcommands: up, down, ls, cycle-scale. A layout literally named "up", "down",
"ls", or "cycle-scale" must be passed as: cmdman compose mux up <name>.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeComposeMuxLayout(cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeMuxUp(cmd, rf, cf, args, flagSession)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)

	composeMuxUpCmd(cmd, rf, cf, &flagSession)
	composeMuxDownCmd(cmd, cf, &flagSession)
	composeMuxLsCmd(cmd, rf, cf, &flagSession)
	composeMuxCycleScaleCmd(cmd, rf, cf, &flagSession)

	parent.AddCommand(cmd)
}

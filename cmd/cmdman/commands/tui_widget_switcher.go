package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/tui"
)

func tuiWidgetSwitcherCmd(parent *cobra.Command, rf *rootFlags, noQuit *bool) {
	var (
		flagWorkDir  string
		flagMuxToken string
	)

	cmd := &cobra.Command{
		Use:   "switcher",
		Short: "Project switcher: every project with its commands under it",
		Long: `Run the project switcher widget.

The switcher lists every known compose project — running, exited, and never
run — each heading a group with its commands listed under it. j/k (or the
arrow keys) move the selection, enter and a mouse click take you to the
selected project's window — opening one for a project that has none yet — and
q quits unless --no-quit was given.

d tears the selected project's dashboard windows down and leaves its commands
running; D stops and removes those commands, and asks for a y on the hint line
first — any other key takes the question back.`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTuiWidget(cmd, args, rf, cli.TUIWidgetOptions{
				Widget:   tui.WidgetSwitcher,
				WorkDir:  flagWorkDir,
				NoQuit:   *noQuit,
				MuxToken: flagMuxToken,
			})
		},
	}

	cmd.Flags().StringVarP(&flagWorkDir, "workdir", "w", "",
		"Override the effective work directory for compose project discovery")
	cmd.Flags().StringVar(&flagMuxToken, "mux-token", "",
		"Multiplexer window token to take the project from, e.g. tmux's #{window_id}")

	parent.AddCommand(cmd)
}

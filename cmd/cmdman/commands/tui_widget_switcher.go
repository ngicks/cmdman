package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/tui"
)

func tuiWidgetSwitcherCmd(parent *cobra.Command, rf *rootFlags, noQuit *bool) {
	var flagWorkDir string

	cmd := &cobra.Command{
		Use:   "switcher",
		Short: "Project switcher: every project with its commands under it",
		Long: `Run the project switcher widget.

The switcher lists every known compose project — running, exited, and never
run — each heading a group with its commands listed under it. j/k (or the
arrow keys) move the selection, enter and a mouse click switch to the selected
project's window, z takes the frame down, and q quits unless --no-quit was
given.`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTuiWidget(cmd, args, rf, tui.WidgetSwitcher, flagWorkDir, *noQuit)
		},
	}

	cmd.Flags().StringVarP(&flagWorkDir, "workdir", "w", "",
		"Override the effective work directory for compose project discovery")

	parent.AddCommand(cmd)
}

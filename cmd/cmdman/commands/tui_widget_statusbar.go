package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/tui"
)

func tuiWidgetStatusbarCmd(parent *cobra.Command, rf *rootFlags, noQuit *bool) {
	var flagWorkDir string

	cmd := &cobra.Command{
		Use:   "statusbar",
		Short: "One-line status bar: the current project and what is running",
		Long: `Run the status bar widget.

The bar is a single line: the working directory's compose project on the left,
the counts across every project next to it, and the cmdman version at the right
edge. It is sized for a one-row pane; q quits unless --no-quit was given.`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTuiWidget(cmd, args, rf, tui.WidgetStatusbar, flagWorkDir, *noQuit)
		},
	}

	cmd.Flags().StringVarP(&flagWorkDir, "workdir", "w", "",
		"Override the effective work directory for compose project discovery")

	parent.AddCommand(cmd)
}

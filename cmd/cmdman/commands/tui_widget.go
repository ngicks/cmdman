package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/tui"
)

func tuiWidgetCmd(parent *cobra.Command, rf *rootFlags) {
	var flagNoQuit bool

	cmd := &cobra.Command{
		Use:   "widget",
		Short: "Run a single TUI widget standalone",
		Long: `Run one TUI widget on its own: a single view filling the terminal, with no
tab bar and no tab switching.

Each widget is a subcommand. A frame definition referencing a built-in
component resolves it to exactly this invocation, so a widget is also
debuggable by hand: run it in any terminal or pane. A widget run as a frame
component always gets --no-quit: a pane whose widget exits would leave a hole
in the frame.`,
	}

	cmd.PersistentFlags().BoolVar(&flagNoQuit, "no-quit", false,
		"Unbind the quit keys, so no keypress ends the widget")

	tuiWidgetSwitcherCmd(cmd, rf, &flagNoQuit)
	tuiWidgetLauncherCmd(cmd, rf, &flagNoQuit)

	parent.AddCommand(cmd)
}

// runTuiWidget is shared by every widget subcommand: they differ only in which
// widget they name.
func runTuiWidget(
	cmd *cobra.Command,
	_ []string,
	rf *rootFlags,
	widget tui.Widget,
	workDir string,
	noQuit bool,
) error {
	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	return cli.RunTUIWidget(cmd.Context(), svc, widget, workDir, noQuit)
}

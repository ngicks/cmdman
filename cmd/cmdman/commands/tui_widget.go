package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/tui"
)

func tuiWidgetCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	cmd := &cobra.Command{
		Use:   "widget",
		Short: "Run a single TUI widget standalone",
		Long: `Run one TUI widget on its own: a single view filling the terminal, with no
tab bar and no tab switching.

Each widget is a subcommand. A frame definition referencing a built-in
component resolves it to exactly this invocation, so a widget is also
debuggable by hand: run it in any terminal or pane.`,
	}

	tuiWidgetSwitcherCmd(cmd, rootCfg)
	tuiWidgetStatusbarCmd(cmd, rootCfg)
	tuiWidgetLauncherCmd(cmd, rootCfg)

	parent.AddCommand(cmd)
}

// runTuiWidget is shared by every widget subcommand: they differ only in which
// widget they name.
func runTuiWidget(
	cmd *cobra.Command,
	_ []string,
	rootCfg *cmdman.CmdmanConfig,
	widget tui.Widget,
	workDir string,
) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	return cli.RunTUIWidget(cmd.Context(), svc, widget, workDir)
}

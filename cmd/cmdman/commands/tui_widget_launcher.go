package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/tui"
)

func tuiWidgetLauncherCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flagWorkDir string

	cmd := &cobra.Command{
		Use:   "launcher",
		Short: "Quick-launch selector: locations left, their compose projects right",
		Long: `Run the quick-launch selector.

The left pane lists target locations — the directories you have brought projects
up in, most recent first, plus everything the filter reaches; the right pane
lists the compose projects at the location under the cursor, toggled on or off.
Type to filter, tab completes the path, enter steps input -> locations ->
projects, esc walks back and then dismisses. On a list, s starts the enabled
projects and S launches and lands in one; in the input every key is text, so
ctrl+c is the dismissal that always works.

The selector fills its window, so the popup framing belongs to the multiplexer.
Bind it as a tmux popup to summon it from anywhere:

  bind-key -n M-Space display-popup -E -w 80% -h 60% 'cmdman tui widget launcher'`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTuiWidget(cmd, args, rootCfg, tui.WidgetLauncher, flagWorkDir)
		},
	}

	cmd.Flags().StringVarP(&flagWorkDir, "workdir", "w", "",
		"Override the effective work directory for compose project discovery")

	parent.AddCommand(cmd)
}

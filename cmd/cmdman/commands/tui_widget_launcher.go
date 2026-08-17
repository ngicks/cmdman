package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/tui"
)

func tuiWidgetLauncherCmd(parent *cobra.Command, rf *rootFlags, noQuit *bool) {
	var flagWorkDir string

	cmd := &cobra.Command{
		Use:   "launcher",
		Short: "Quick-launch selector: locations left, their compose projects right",
		Long: `Run the quick-launch selector.

The left pane lists target locations — with the input empty, the directories you
have brought projects up in, most recent first, then the directories of the
projects named under cmdman's config compose/ directory, sorted by the name each
row shows; typing widens that to everything the filter reaches. The right pane
lists the compose projects at the location under the cursor, toggled on or off:
one brought up before arrives on, one known only from the config compose/
directory arrives off until space turns it on. Type to filter; tab completes the
path over the locations and the directories on disk, and lists the candidates it
cannot choose between — tab and shift+tab then put them in the input one at a
time and enter accepts the one it stands on.
Otherwise enter steps input -> locations -> projects, and esc drops the list —
taking back whatever it put in the input — before it walks the zones back and
dismisses. On a list, s starts the enabled projects and S launches and lands in
one; d tears a project's dashboard down and leaves its commands running, and D
stops and removes those commands after a y on the note line — any other key
takes the question back. In the input every key is text, so ctrl+c is the
dismissal that works from anywhere (unless --no-quit took the quit keys away).

The selector fills its window, so the popup framing belongs to the multiplexer.
Bind it as a tmux popup to summon it from anywhere:

  bind-key -n M-Space display-popup -E -w 80% -h 60% 'cmdman tui widget launcher'`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTuiWidget(cmd, args, rf, cli.TUIWidgetOptions{
				Widget:  tui.WidgetLauncher,
				WorkDir: flagWorkDir,
				NoQuit:  *noQuit,
			})
		},
	}

	cmd.Flags().StringVarP(&flagWorkDir, "workdir", "w", "",
		"Override the effective work directory for compose project discovery")

	parent.AddCommand(cmd)
}

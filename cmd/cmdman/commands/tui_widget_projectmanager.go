package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/tui"
)

func tuiWidgetProjectManagerCmd(parent *cobra.Command, rf *rootFlags, noQuit *bool) {
	var (
		flagWorkDir     string
		flagMuxToken    string
		flagFile        string
		flagProjectName string
	)

	cmd := &cobra.Command{
		Use:   "project-manager",
		Short: "Project shortcuts: replica scale, shown replica, and layout",
		Long: `Run the project-manager widget.

The panel is a shortcut over one project: set a service's replica count, cycle
which replica a scaled command's dashboard pane shows, and cycle or apply one of
the project's mux layouts. Every action wraps the command that already does it,
so nothing here is reachable only from the widget.

Keys:

  j/k, up/down     move in the focused list
  tab              switch focus: services or layouts
  +/=, -           services: replica count up / down
  l/right, h/left  services: show the next / previous replica
  enter            layouts: apply the selected layout
  c                layouts: cycle to the next layout
  r                reload
  q, ctrl+c        quit (absent under --no-quit)

Showing the previous replica needs the shown one to be known; where a project's
dashboard windows disagree about it, only l/right applies.

The project is the one the panel detects: the window --mux-token names, else the
window the panel runs in, else the project of the working directory. --file and
--project-name name one explicitly, skipping detection; --workdir says which
directory that project stands in, and is part of the explicit target as much as
of the working-directory probe — a compose file names a project only together
with its work directory, which is where its commands are found.

The panel fills its window, so the popup framing belongs to the multiplexer.
tmux does not expand formats in a display-popup shell-command, so bind it
through run-shell, which is expanded, to pass the window it was summoned from:

  bind-key -n M-p run-shell 'tmux display-popup -E -w 80% -h 60% \
    "cmdman tui widget project-manager --mux-token #{window_id}"'`,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTuiWidget(cmd, args, rf, cli.TUIWidgetOptions{
				Widget:      tui.WidgetProjectManager,
				WorkDir:     flagWorkDir,
				NoQuit:      *noQuit,
				MuxToken:    flagMuxToken,
				File:        flagFile,
				ProjectName: flagProjectName,
			})
		},
	}

	cmd.Flags().StringVarP(&flagWorkDir, "workdir", "w", "",
		"Override the effective work directory for compose project discovery")
	cmd.Flags().StringVar(&flagMuxToken, "mux-token", "",
		"Multiplexer window token to take the project from, e.g. tmux's #{window_id}")
	cmd.Flags().StringVarP(&flagFile, "file", "f", "",
		"Compose file path of the project to manage")
	cmd.Flags().StringVarP(&flagProjectName, "project-name", "p", "",
		"Project name to manage (overrides YAML name:)")
	_ = cmd.RegisterFlagCompletionFunc("file", completeComposeFile)

	parent.AddCommand(cmd)
}

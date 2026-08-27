package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
)

type captureScreenFlags struct {
	Escapes                bool
	AltScreen              bool
	Quiet                  bool
	PreserveTrailingSpaces bool
	StartLine              string
	EndLine                string
}

func captureScreenCmd(parent *cobra.Command, rf *rootFlags) {
	var f captureScreenFlags

	cmd := &cobra.Command{
		// No alias: capture-screen is spelled out on purpose so the vocabulary
		// stays on the one name, and a bare `capture` would read as if cmdman
		// had panes to capture from.
		Use: "capture-screen [flags] ID|NAME",
		Short: "Capture a snapshot of a running TTY command's screen " +
			"(mirrors tmux capture-pane)",
		Long: `Capture a snapshot of a running TTY command's screen.

The flags mirror tmux's capture-pane. Only a command created with a TTY has a
screen to capture; for one without, read its output with cmdman logs.`,
		Args: cobra.ExactArgs(1),
		// Only one positional is a command target; nothing follows it.
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]cobra.Completion, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeCommandNames(rf, runningStates...)(cmd, args, toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCaptureScreen(cmd, args, rf, &f)
		},
	}

	flags := cmd.Flags()
	flags.BoolVarP(&f.Escapes, "escapes", "e", false,
		"Include escape sequences for text and background attributes")
	flags.BoolVarP(&f.AltScreen, "alt-screen", "a", false,
		"Capture the alternate screen; errors when the command has none")
	flags.BoolVarP(&f.Quiet, "quiet", "q", false,
		"With -a, succeed with empty output when there is no alternate screen")
	flags.BoolVarP(&f.PreserveTrailingSpaces, "preserve-trailing-spaces", "N", false,
		"Preserve trailing spaces at each line's end")
	flags.StringVarP(&f.StartLine, "start-line", "S", "",
		"First line to capture: 0 is the top visible row, negative numbers reach "+
			"into history, '-' is the start of history")
	flags.StringVarP(&f.EndLine, "end-line", "E", "",
		"Last line to capture: '-' is the bottom of the visible screen")

	parent.AddCommand(cmd)
}

func runCaptureScreen(
	cmd *cobra.Command,
	args []string,
	rf *rootFlags,
	f *captureScreenFlags,
) error {
	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	content, err := svc.CaptureScreen(cmd.Context(), args[0], cmdman.CaptureScreenRequest{
		Escapes:                f.Escapes,
		AltScreen:              f.AltScreen,
		Quiet:                  f.Quiet,
		PreserveTrailingSpaces: f.PreserveTrailingSpaces,
		StartLine:              f.StartLine,
		EndLine:                f.EndLine,
	})
	if err != nil {
		return err
	}

	_, err = cmd.OutOrStdout().Write(content)
	return err
}

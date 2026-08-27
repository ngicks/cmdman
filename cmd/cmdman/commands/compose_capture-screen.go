package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeCaptureScreenCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		f         captureScreenFlags
		flagScale int
	)

	cmd := &cobra.Command{
		Use: "capture-screen [flags] SERVICE",
		Short: "Capture a snapshot of a running compose command's screen " +
			"(mirrors tmux capture-pane)",
		Long: `Capture a snapshot of a running compose command's screen.

The flags mirror tmux's capture-pane. Only a command created with a TTY has a
screen to capture; for one without, read its output with cmdman compose logs.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeCaptureScreen(cmd, rf, cf, args[0], &f, flagScale)
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
	flags.IntVar(
		&flagScale,
		"scale",
		0,
		"Scale index (1-based) of the replica to capture; required when the service has >1 replica",
	)

	parent.AddCommand(cmd)
}

func runComposeCaptureScreen(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	serviceName string,
	f *captureScreenFlags,
	scaleIndex int,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	content, err := compose.NewService(svc).CaptureScreen(
		cmd.Context(),
		selection,
		compose.CaptureScreenOption{
			CommandName:           serviceName,
			ScaleIndex:            scaleIndex,
			Escapes:               f.Escapes,
			AltScreen:             f.AltScreen,
			Quiet:                 f.Quiet,
			PreserveTrailingSpace: f.PreserveTrailingSpaces,
			StartLine:             f.StartLine,
			EndLine:               f.EndLine,
		},
	)
	if err != nil {
		return err
	}

	_, err = cmd.OutOrStdout().Write(content)
	return err
}

package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeStatusCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagFormat string
		flagScale  int
	)

	cmd := &cobra.Command{
		Use:   "status [COMMAND...]",
		Short: "Show what the commands of a compose project report about themselves",
		Long: `Show what the commands of a compose project report about themselves.

This is the project-wide read of ` + "`cmdman status get`" + `: the reported status and
detail of every command, plus the title it last set and whether its bell is
still unread. Commands that are not running have nothing to report.

Writing a status is per command - see ` + "`cmdman status set`" + `.`,
		Args:              scaleArgs(cobra.ArbitraryArgs, &flagScale),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeStatus(cmd, rf, cf, args, flagScale, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.ComposeStatusFormatUsage())
	addScaleFlag(cmd, &flagScale)

	parent.AddCommand(cmd)
}

func runComposeStatus(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	scale int,
	format string,
) error {
	selection, err := compose.LoadOrWorkdir(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	states, err := compose.NewService(svc).Status(cmd.Context(), selection, compose.StatusOption{
		Targets: composeTargets(commandNames, scale),
	})
	if err != nil {
		return err
	}

	return cli.RenderComposeStatus(cmd.OutOrStdout(), states, format)
}

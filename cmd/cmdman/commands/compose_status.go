package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeStatusCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:   "status [COMMAND...]",
		Short: "Show what the commands of a compose project report about themselves",
		Long: `Show what the commands of a compose project report about themselves.

This is the project-wide read of ` + "`cmdman status get`" + `: the reported status and
detail of every command, plus the title it last set and whether its bell is
still unread. Commands that are not running have nothing to report.

Writing a status is per command - see ` + "`cmdman status set`" + `.`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rootCfg, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeStatus(cmd, rootCfg, cf, args, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.ComposeStatusFormatUsage())

	parent.AddCommand(cmd)
}

func runComposeStatus(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	format string,
) error {
	selection, err := compose.LoadOrWorkdir(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	states, err := compose.NewService(svc).Status(cmd.Context(), selection, commandNames)
	if err != nil {
		return err
	}

	return cli.RenderComposeStatus(cmd.OutOrStdout(), states, format)
}

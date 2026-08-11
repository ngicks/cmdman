package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeLsCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:               "ls",
		Short:             "List compose projects",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeLs(cmd, rf, flagFormat)
		},
	}

	cmd.Flags().StringVar(&flagFormat, "format", "", cli.ComposeLsFormatUsage())

	parent.AddCommand(cmd)
}

func runComposeLs(
	cmd *cobra.Command,
	rf *rootFlags,
	format string,
) error {
	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	summaries, err := compose.NewService(svc).ListProjects(cmd.Context())
	if err != nil {
		return err
	}

	return cli.RenderComposeProjects(cmd.OutOrStdout(), summaries, format)
}

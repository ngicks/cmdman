package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/cli"
)

func lsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagLabel  []string
		flagAll    bool
		flagQuiet  bool
		flagFormat string
	)

	cmd := &cobra.Command{
		Use:               "ls [flags]",
		Short:             "List commands",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLs(cmd, args, rootCfg, flagLabel, flagAll, flagQuiet, flagFormat)
		},
	}

	cmd.Flags().
		StringArrayVarP(&flagLabel, "label", "l", nil, "Filter by label KEY=VALUE (repeatable)")
	cmd.Flags().BoolVarP(&flagAll, "all", "a", false, "Show all (including exited)")
	cmd.Flags().BoolVarP(&flagQuiet, "quiet", "q", false, "Print IDs only")
	cmd.Flags().StringVar(&flagFormat, "format", "", cli.FormatUsage())

	parent.AddCommand(cmd)
}

func runLs(
	cmd *cobra.Command,
	_ []string,
	rootCfg *cmdman.CmdmanConfig,
	labelSlice []string,
	allStates, quiet bool,
	format string,
) error {
	labels, err := parseLabels(labelSlice)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	entries, err := svc.List(cmd.Context(), cmdman.ListRequest{
		AllStates: allStates,
		Labels:    labels,
	})
	if err != nil {
		return err
	}

	var runtime map[string]cmdman.RuntimeState
	if !quiet {
		// Quiet mode prints IDs only, so it never pays for the dial.
		runtime = cli.RuntimeStates(cmd.Context(), svc, entries)
	}

	return cli.RenderEntries(cmd.OutOrStdout(), entries, runtime, quiet, format)
}

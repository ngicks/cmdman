package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

// tuiPopupFlag captures the bool-style optional-value --popup flag: whether it
// was given, and its value ("true" for bare --popup, or "tmux"/"zellij").
type tuiPopupFlag struct {
	set   bool
	value string
}

func tuiCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var popup tuiPopupFlag

	cmd := &cobra.Command{
		Use:               "tui",
		Short:             "Interactive terminal UI for compose-managed commands",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTui(cmd, args, rootCfg, popup)
		},
	}

	cmd.Flags().BoolFunc(
		"popup",
		"Run the TUI in a multiplexer popup (v1: tmux only); optional value tmux|zellij",
		func(s string) error {
			popup.set = true
			popup.value = s
			return nil
		},
	)

	tuiChildCmd(cmd, rootCfg)

	parent.AddCommand(cmd)
}

func runTui(
	cmd *cobra.Command,
	_ []string,
	rootCfg *cmdman.CmdmanConfig,
	popup tuiPopupFlag,
) error {
	if popup.set {
		return cli.LaunchTUIPopup(cmd.Context(), popup.value, rootCfg.DataDir, rootCfg.RuntimeDir)
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	return cli.RunTUI(cmd.Context(), svc)
}

// tuiChildCmd registers the hidden `cmdman tui __child` subcommand that runs
// the actual TUI inside a multiplexer popup and reports status to the launcher
// over IPC. It is internal and excluded from help and completion.
func tuiChildCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flagIPC string

	cmd := &cobra.Command{
		Use:               "__child",
		Short:             "Internal: run the TUI inside a multiplexer popup",
		Hidden:            true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTuiChild(cmd, args, rootCfg, flagIPC)
		},
	}

	cmd.Flags().StringVar(&flagIPC, "ipc", "", "IPC endpoint for the popup launcher")

	parent.AddCommand(cmd)
}

func runTuiChild(cmd *cobra.Command, _ []string, rootCfg *cmdman.CmdmanConfig, ipc string) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	return cli.RunTUIChild(cmd.Context(), svc, ipc)
}

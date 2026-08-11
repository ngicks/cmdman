package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/tui"
)

// tuiPopupFlag captures the bool-style optional-value --popup flag: whether it
// was given, and its value ("true" for bare --popup, or "tmux"/"zellij").
type tuiPopupFlag struct {
	set   bool
	value string
}

func tuiCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		popup       tuiPopupFlag
		flagTab     string
		flagWorkDir string
		geom        cli.PopupGeometry
	)

	cmd := &cobra.Command{
		Use:               "tui",
		Short:             "Interactive terminal UI for compose-managed commands",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTui(cmd, args, rf, popup, flagTab, flagWorkDir, geom)
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
	cmd.Flags().StringVar(&flagTab, "tab", "commands",
		"Tab shown on startup: "+strings.Join(tui.TabKeys(), ", "))
	_ = cmd.RegisterFlagCompletionFunc("tab", tabCompletions)

	cmd.Flags().StringVarP(&flagWorkDir, "workdir", "w", "",
		"Override the effective work directory for compose project discovery")

	cmd.Flags().StringVar(&geom.Width, "popup-width", "",
		"Popup width as an explicit percentage, e.g. 80% (requires --popup)")
	cmd.Flags().StringVar(&geom.Height, "popup-height", "",
		"Popup height as an explicit percentage, e.g. 80% (requires --popup)")
	cmd.Flags().StringVar(&geom.X, "popup-x", "",
		"Popup X position as an explicit percentage, e.g. 10% (requires --popup)")
	cmd.Flags().StringVar(&geom.Y, "popup-y", "",
		"Popup Y position as an explicit percentage, e.g. 10% (requires --popup)")

	tuiWidgetCmd(cmd, rf)
	tuiChildCmd(cmd, rf)

	parent.AddCommand(cmd)
}

func runTui(
	cmd *cobra.Command,
	_ []string,
	rf *rootFlags,
	popup tuiPopupFlag,
	flagTab string,
	flagWorkDir string,
	geom cli.PopupGeometry,
) error {
	tab, err := tui.ParseTab(flagTab)
	if err != nil {
		return err
	}

	if !popup.set {
		for _, name := range []string{"popup-width", "popup-height", "popup-x", "popup-y"} {
			if cmd.Flags().Changed(name) {
				return fmt.Errorf("--%s only applies with --popup", name)
			}
		}
	}

	if popup.set {
		// The popup child re-execs cmdman, so it gets the dirs this process
		// resolved rather than resolving them again from its own environment.
		cfg, err := loadConfig(cmd, rf)
		if err != nil {
			return err
		}
		return cli.LaunchTUIPopup(
			cmd.Context(), popup.value, cfg.DataDir, cfg.RuntimeDir, tab, flagWorkDir, geom)
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	return cli.RunTUI(cmd.Context(), svc, tab, flagWorkDir)
}

// tuiChildCmd registers the hidden `cmdman tui __child` subcommand that runs
// the actual TUI inside a multiplexer popup and reports status to the launcher
// over IPC. It is internal and excluded from help and completion.
func tuiChildCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		flagIPC     string
		flagTab     string
		flagWorkDir string
	)

	cmd := &cobra.Command{
		Use:               "__child",
		Short:             "Internal: run the TUI inside a multiplexer popup",
		Hidden:            true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTuiChild(cmd, args, rf, flagIPC, flagTab, flagWorkDir)
		},
	}

	cmd.Flags().StringVar(&flagIPC, "ipc", "", "IPC endpoint for the popup launcher")
	cmd.Flags().StringVar(&flagTab, "tab", "commands",
		"Tab shown on startup: "+strings.Join(tui.TabKeys(), ", "))
	cmd.Flags().StringVarP(&flagWorkDir, "workdir", "w", "",
		"Override the effective work directory for compose project discovery")

	parent.AddCommand(cmd)
}

func runTuiChild(
	cmd *cobra.Command,
	_ []string,
	rf *rootFlags,
	ipc, flagTab, flagWorkDir string,
) error {
	tab, err := tui.ParseTab(flagTab)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	return cli.RunTUIChild(cmd.Context(), svc, ipc, tab, flagWorkDir)
}

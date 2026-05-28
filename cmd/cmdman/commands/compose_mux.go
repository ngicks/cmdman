package commands

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

func composeMuxCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	cmd := &cobra.Command{
		Use:   "mux",
		Short: "Open a multiplexer dashboard for a compose project",
		Long: `Open a multiplexer dashboard described by the compose file's "mux:"
section. Each leaf references a compose service name; panes run
cmdman attach by default, or cmdman logs when mode: logs.

The compose file must contain a top-level "mux:" section; a missing section
is an error (no synthesized default).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeMux(cmd, rootCfg, cf)
		},
	}
	parent.AddCommand(cmd)
}

func runComposeMux(cmd *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}
	if selection.Spec == nil {
		return errors.New("compose mux: no compose file found")
	}
	if selection.Spec.Mux == nil {
		return errors.New(`compose mux: missing "mux:" section in compose file`)
	}

	spec, err := mux.DecodeNode(selection.Spec.Mux)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	cfg := svc.Config()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cmdman binary: %w", err)
	}

	composeSvc := compose.NewService(svc)
	resolver := func(ctx context.Context, leafName string) (string, error) {
		return composeSvc.ResolveCommandID(ctx, selection, leafName)
	}

	built, err := mux.Build(cmd.Context(), spec, resolver, mux.PaneArgvOpts{
		Executable: exe,
		DataDir:    cfg.DataDir,
		RuntimeDir: cfg.RuntimeDir,
	})
	if err != nil {
		return err
	}

	windowName := "cmdman"
	if selection.Project != "" {
		windowName = "cmdman-" + selection.Project
	}
	return mux.Run(cmd.Context(), built, mux.RunOptions{
		WindowName: windowName,
		Stdout:     cmd.OutOrStdout(),
	})
}

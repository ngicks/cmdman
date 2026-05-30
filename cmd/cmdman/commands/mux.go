package commands

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

func muxCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	cmd := &cobra.Command{
		Use:   "mux [path]",
		Short: "Open a multiplexer dashboard for cmdman commands",
		Long: `Open a multiplexer dashboard described by a layout file. Each leaf
references a cmdman command (by ID or NAME); panes run cmdman attach by
default, or cmdman logs when mode: logs.

The layout file is a YAML document with a top-level mux: section. With no
path argument (or "-"), the spec is read from stdin.`,
		Args: cobra.MaximumNArgs(1),
		// The positional arg is a layout file path; the shell's default file
		// completion is the right behavior, so ValidArgsFunction is left unset.
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "-"
			if len(args) == 1 {
				path = args[0]
			}
			return runMux(cmd, rootCfg, path)
		},
	}
	parent.AddCommand(cmd)
}

func runMux(cmd *cobra.Command, rootCfg *cmdman.CmdmanConfig, path string) error {
	src, closer, err := openSpecSource(path)
	if err != nil {
		return err
	}
	defer closer()

	spec, err := mux.Decode(src)
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

	resolver := func(ctx context.Context, leafName string) (string, error) {
		out, err := svc.Inspect(ctx, leafName)
		if err != nil {
			return "", err
		}
		return out.ID, nil
	}

	built, err := mux.Build(cmd.Context(), spec, resolver, mux.PaneArgvOpts{
		Executable: exe,
		DataDir:    cfg.DataDir,
		RuntimeDir: cfg.RuntimeDir,
	})
	if err != nil {
		return err
	}

	return mux.Run(cmd.Context(), built, mux.RunOptions{
		Stdout: cmd.OutOrStdout(),
	})
}

// openSpecSource opens the spec source. An empty or "-" path reads from stdin
// (returns a no-op closer); anything else opens the file.
func openSpecSource(path string) (io.Reader, func(), error) {
	if path == "" || path == "-" {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}

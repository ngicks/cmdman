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
	var (
		flagSession string
		flagDetach  bool
	)

	cmd := &cobra.Command{
		Use:   "mux [path]",
		Short: "Open a multiplexer dashboard for cmdman commands",
		Long: `Open a multiplexer dashboard described by a layout file. Each leaf
references a cmdman command (by ID or NAME); panes run cmdman attach by
default, or cmdman logs when mode: logs.

The layout file is a YAML document with a top-level mux: section. With no
path argument (or "-"), the spec is read from stdin.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".

With --detach, the dashboard window is torn down instead of opened: the
in-pane viewers are detached, the window collapses to a single clean pane,
and the tmux options cmdman set (pane-border-status, @cmdman_marker) are
cleared. The supervised commands keep running. Pass the same file when the
dashboard used a custom driver_opt.socket so detach targets the right server.`,
		Args: cobra.MaximumNArgs(1),
		// The positional arg is a layout file path; the shell's default file
		// completion is the right behavior, so ValidArgsFunction is left unset.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMux(cmd, rootCfg, args, flagSession, flagDetach)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)
	cmd.Flags().BoolVar(
		&flagDetach, "detach", false,
		"Tear down the dashboard: restore the window to one clean pane and clear cmdman's tmux options",
	)
	parent.AddCommand(cmd)
}

func runMux(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	args []string,
	session string,
	detach bool,
) error {
	path := "-"
	if len(args) == 1 {
		path = args[0]
	}

	if detach {
		return runMuxDetach(cmd, path, session)
	}

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

	// Standalone mux leaves name concrete cmdman commands, so there is no replica
	// cycling (nil counter). A leaf may still pin an explicit scale index, which
	// resolves the suffixed command name "<leaf>-<scaleIndex>".
	resolver := func(ctx context.Context, leafName string, scaleIndex int) (string, error) {
		target := leafName
		if scaleIndex > 0 {
			target = fmt.Sprintf("%s-%d", leafName, scaleIndex)
		}
		out, err := svc.Inspect(ctx, target)
		if err != nil {
			return "", err
		}
		return out.ID, nil
	}

	built, err := mux.Build(cmd.Context(), spec, resolver, nil, mux.PaneArgvOpts{
		Executable: exe,
		DataDir:    cfg.DataDir,
		RuntimeDir: cfg.RuntimeDir,
	})
	if err != nil {
		return err
	}

	return mux.Run(cmd.Context(), built, mux.RunOptions{
		SessionName: session,
		Stdout:      cmd.OutOrStdout(),
	})
}

// runMuxDetach tears the dashboard down instead of building it. It needs no
// cmdman service or leaf resolution — only the spec's driver / driver_opt to
// know which multiplexer server to target. The spec is read only when an
// explicit file path is given; with the stdin default ("-") detach uses the
// default driver rather than blocking on stdin.
func runMuxDetach(cmd *cobra.Command, path, session string) error {
	var (
		driver    string
		driverOpt map[string]string
	)
	if path != "-" {
		src, closer, err := openSpecSource(path)
		if err != nil {
			return err
		}
		defer closer()
		spec, err := mux.Decode(src)
		if err != nil {
			return err
		}
		driver, driverOpt = spec.Driver, spec.DriverOpt
	}

	return mux.Detach(cmd.Context(), mux.DetachOptions{
		Driver:      driver,
		DriverOpt:   driverOpt,
		SessionName: session,
		Stdout:      cmd.OutOrStdout(),
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

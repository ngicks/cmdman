package commands

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

func muxCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "mux [path]",
		Short: "Open a multiplexer dashboard for cmdman commands",
		Long: `Open a multiplexer dashboard described by a layout file (alias of "mux up").

Each leaf references a cmdman command (by ID or NAME); panes run cmdman attach
by default, or cmdman logs when mode: logs.

The layout file is a YAML document with a top-level mux: section. With no path
argument (or "-"), the spec is read from stdin.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".

Subcommands: up, down, ls. A path literally named "up", "down", or "ls" must
be passed as: cmdman mux up <path>.`,
		Args: cobra.MaximumNArgs(1),
		// The positional arg is a layout file path; the shell's default file
		// completion is the right behavior, so ValidArgsFunction is left unset.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMuxUp(cmd, rootCfg, args, flagSession)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)

	muxUpCmd(cmd, rootCfg, &flagSession)
	muxDownCmd(cmd, &flagSession)
	muxLsCmd(cmd, &flagSession)

	parent.AddCommand(cmd)
}

func muxUpCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "up [path]",
		Short: "Open a multiplexer dashboard for cmdman commands",
		Long: `Open a multiplexer dashboard described by a layout file.

Each leaf references a cmdman command (by ID or NAME); panes run cmdman attach
by default, or cmdman logs when mode: logs.

The layout file is a YAML document with a top-level mux: section. With no path
argument (or "-"), the spec is read from stdin.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".`,
		Args: cobra.MaximumNArgs(1),
		// The positional arg is a layout file path; file completion is appropriate.
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxUp(cmd, rootCfg, args, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)

	parent.AddCommand(cmd)
}

func muxDownCmd(parent *cobra.Command, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "down [path]",
		Short: "Tear down the dashboard windows for this spec",
		Long: `Tear down the cmdman-owned dashboard windows matching this spec's identity.

The in-pane viewers are detached, the window collapses to a single clean pane,
and the tmux options cmdman set are cleared. The supervised commands keep
running — only the disposable viewers are torn down.

A layout file path is optional: it is only read to extract the driver and
driver_opt (e.g. a custom socket). With no path (or the stdin default "-"),
teardown uses the default driver with no custom options.

Window discovery is server-wide and requires no $TMUX context; it works from
any pane, run-shell, or outside tmux. --session narrows the scan to one session.`,
		Args: cobra.MaximumNArgs(1),
		// The positional arg is an optional layout file; file completion is appropriate.
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxDown(cmd, args, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow teardown to this tmux session only (default: server-wide)",
	)

	parent.AddCommand(cmd)
}

func muxLsCmd(parent *cobra.Command, parentSession *string) {
	var (
		flagSession string
		flagFormat  string
	)

	cmd := &cobra.Command{
		Use:   "ls [path]",
		Short: "List all cmdman-owned dashboard windows",
		Args:  cobra.MaximumNArgs(1),
		// The positional arg is an optional layout file path; file completion is appropriate.
		Long: `List all cmdman-owned dashboard windows on the server.

Discovery is server-wide and requires no $TMUX context; it works from any
pane, run-shell, or outside tmux. --session narrows the listing to one session.

A layout file path is optional: when given it is read only to extract the
driver and driver_opt (for example a custom socket). With no path or the stdin
default "-", listing uses the default driver with no custom options.

Columns: SESSION, WINDOW, ID, IDENTITY, LAYOUT (-1 displayed as "-").`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runMuxLs(cmd, args, sess, flagFormat)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow listing to this tmux session only (default: server-wide)",
	)
	cmd.Flags().StringVar(&flagFormat, "format", "", cli.MuxLsFormatUsage())

	parent.AddCommand(cmd)
}

func runMuxUp(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	args []string,
	session string,
) error {
	path := "-"
	if len(args) == 1 {
		path = args[0]
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

	// Standalone mux names concrete cmdman commands, so there is no replica
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

// runMuxDown tears the dashboard down instead of building it. The spec path is
// optional: it is only read when an explicit path is given, to extract the
// driver and driver_opt. With the stdin default ("-") teardown uses the default
// driver rather than blocking on stdin.
func runMuxDown(cmd *cobra.Command, args []string, session string) error {
	path := "-"
	if len(args) == 1 {
		path = args[0]
	}

	driver, driverOpt, err := specDriverOpts(path)
	if err != nil {
		return err
	}

	return mux.Down(cmd.Context(), mux.DownOptions{
		Driver:      driver,
		DriverOpt:   driverOpt,
		SessionName: session,
		Stdout:      cmd.OutOrStdout(),
	})
}

// specDriverOpts extracts the driver and driver_opt fields from the spec at
// path. It is used by runMuxDown and runMuxLs to honour a custom socket when
// one is declared in the spec without requiring the caller to resolve the full
// layout. The stdin default ("-") is treated as no file, so teardown/listing
// uses the default driver rather than blocking on stdin.
func specDriverOpts(path string) (driver string, driverOpt map[string]string, err error) {
	if path == "-" {
		return "", nil, nil
	}
	src, closer, err := openSpecSource(path)
	if err != nil {
		return "", nil, err
	}
	defer closer()
	spec, err := mux.Decode(src)
	if err != nil {
		return "", nil, err
	}
	return spec.Driver, spec.DriverOpt, nil
}

func runMuxLs(cmd *cobra.Command, args []string, session, format string) error {
	path := "-"
	if len(args) == 1 {
		path = args[0]
	}

	driver, driverOpt, err := specDriverOpts(path)
	if err != nil {
		return err
	}

	windows, err := mux.List(cmd.Context(), mux.ListOptions{
		Driver:      driver,
		DriverOpt:   driverOpt,
		SessionName: session,
	})
	if err != nil {
		return err
	}
	return cli.RenderMuxWindows(cmd.OutOrStdout(), windows, format)
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

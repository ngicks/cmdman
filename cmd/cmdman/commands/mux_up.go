package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/mux"
)

func muxUpCmd(parent *cobra.Command, rf *rootFlags, parentSession *string) {
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
			return runMuxUp(cmd, rf, args, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)

	parent.AddCommand(cmd)
}

func runMuxUp(
	cmd *cobra.Command,
	rf *rootFlags,
	args []string,
	session string,
) error {
	return cli.RunMuxOp(cmd.Context(), func(ctx context.Context) error {
		return muxUpOp(ctx, cmd, rf, args, session)
	})
}

// muxUpOp is everything `mux up` does, split out so [cli.RunMuxOp] decides
// where it runs: here, or in a worker that outlives the invoking pane.
func muxUpOp(
	ctx context.Context,
	cmd *cobra.Command,
	rf *rootFlags,
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

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	cfg := svc.Config()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cmdman binary: %w", err)
	}

	built, err := mux.Build(ctx, mux.BuildOptions{
		Spec: spec,
		// Standalone mux names concrete cmdman commands, so there is no replica
		// cycling (nil counter). A leaf may still pin an explicit scale index,
		// which resolves the suffixed command name "<leaf>-<scaleIndex>".
		Resolver: svc.MuxResolver(),
		Replicas: nil,
		Opts: mux.PaneArgvOpts{
			Executable: exe,
			DataDir:    cfg.DataDir,
			RuntimeDir: cfg.RuntimeDir,
		},
	})
	if err != nil {
		return err
	}

	return mux.Run(ctx, built, mux.RunOptions{
		SessionName: session,
		Config:      cfg,
		Svc:         cli.NewFrameSvc(svc),
		Stdout:      cmd.OutOrStdout(),
	})
}

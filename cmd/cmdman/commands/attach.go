package commands

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/internal/cmdsignals"
	"github.com/ngicks/cmdman/internal/stdiopipe"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

type attachFlags struct {
	NoStdin    bool
	SigProxy   bool
	DetachKeys string
	AutoExit   bool
}

func attachCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flags attachFlags

	cmd := &cobra.Command{
		Use:               "attach [flags] ID|NAME",
		Short:             "Attach to a running command's PTY",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCommandNames(rootCfg, runningStates...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd, args, rootCfg, flags)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&flags.NoStdin, "no-stdin", false, "Output-only mode")
	f.BoolVar(&flags.SigProxy, "sig-proxy", true, "Forward signals to command")
	f.StringVar(&flags.DetachKeys, "detach-keys", "ctrl-p,ctrl-q", "Key sequence to detach")
	f.BoolVar(
		&flags.AutoExit, "auto-exit", false,
		"Exit immediately when the command exits or is not running (opt out of sticky default)",
	)

	parent.AddCommand(cmd)
}

func runAttach(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	flags attachFlags,
) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	attachCtx, cancelAttach := context.WithCancel(cmd.Context())
	defer cancelAttach()

	opts := cli.AttachOptions{
		NoStdin:    flags.NoStdin,
		SigProxy:   flags.SigProxy,
		DetachKeys: flags.DetachKeys,
		// Pause the root SIGINT/SIGTERM handler while attached so those signals
		// forward to the remote command, then restore it on detach.
		PauseSignals: func(install func()) bool {
			return cmdsignals.Pause(cmd.Context(), install)
		},
		ResumeSignals: func(remove func()) bool {
			return cmdsignals.Resume(cmd.Context(), remove)
		},
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		StdinPipe:  stdiopipe.Stdin(attachCtx),
		StdoutPipe: stdiopipe.Stdout(attachCtx),
	}

	if flags.AutoExit {
		session, err := svc.OpenAttachSession(attachCtx, args[0])
		if err != nil {
			return err
		}
		defer func() { _ = session.Close() }()

		err = cli.Attach(attachCtx, session, opts)
		if errors.Is(err, cli.ErrRemoteEOF) {
			// --auto-exit preserves today's silent exit on EOF.
			return nil
		}
		return err
	}

	hooks := cli.StickyHooks{
		State: stickyStateFor(svc, args[0]),
		OpenSession: func(ctx context.Context) (cli.AttachSession, error) {
			return svc.OpenAttachSession(ctx, args[0])
		},
		Restart: func(ctx context.Context) error {
			results, err := svc.Restart(ctx, cmdman.RestartRequest{Targets: []string{args[0]}})
			if err != nil {
				return err
			}
			for _, r := range results {
				if r.Err != nil {
					return r.Err
				}
			}
			return nil
		},
	}
	return cli.AttachSticky(attachCtx, hooks, opts)
}

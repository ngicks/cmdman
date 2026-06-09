package commands

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmd/internal/cmdsignals"
	"github.com/ngicks/cmdman/cmd/internal/stdiopipe"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeAttachCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flags     attachFlags
		flagScale int
	)

	cmd := &cobra.Command{
		Use:               "attach [flags] SERVICE",
		Short:             "Attach to a running compose command's PTY",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeComposeCommands(rootCfg, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeAttach(cmd, rootCfg, cf, args[0], flags, flagScale)
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
	f.IntVar(
		&flagScale,
		"scale",
		0,
		"Scale index (1-based) of the replica to attach to; required when the service has >1 replica",
	)

	parent.AddCommand(cmd)
}

func runComposeAttach(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	serviceName string,
	flags attachFlags,
	scaleIndex int,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	composeSvc := compose.NewService(svc)

	attachCtx, cancelAttach := context.WithCancel(cmd.Context())
	defer cancelAttach()

	opts := cli.AttachOptions{
		NoStdin:      flags.NoStdin,
		SigProxy:     flags.SigProxy,
		DetachKeys:   flags.DetachKeys,
		ResetSignals: cmdsignals.ExitSignals[:],
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		StdinPipe:    stdiopipe.Stdin(attachCtx),
		StdoutPipe:   stdiopipe.Stdout(attachCtx),
	}

	if flags.AutoExit {
		session, err := composeSvc.OpenAttachSession(attachCtx, selection, serviceName, scaleIndex)
		if err != nil {
			return err
		}
		defer func() { _ = session.Close() }()

		err = cli.Attach(attachCtx, session, opts)
		if errors.Is(err, cli.ErrRemoteEOF) {
			return nil
		}
		return err
	}

	id, err := composeSvc.ResolveCommandID(attachCtx, selection, serviceName, scaleIndex)
	if err != nil {
		return err
	}
	hooks := cli.StickyHooks{
		State: stickyStateFor(svc, id),
		OpenSession: func(ctx context.Context) (cli.AttachSession, error) {
			return svc.OpenAttachSession(ctx, id)
		},
		Restart: func(ctx context.Context) error {
			results, err := svc.Restart(ctx, cmdman.RestartRequest{Targets: []string{id}})
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

package commands

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeAttachCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flags     attachFlags
		flagScale int
	)

	cmd := &cobra.Command{
		Use:               "attach [flags] SERVICE",
		Short:             "Attach to a running compose command's PTY",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeAttach(cmd, rf, cf, args[0], flags, flagScale)
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
	rf *rootFlags,
	cf *composeFlags,
	serviceName string,
	flags attachFlags,
	scaleIndex int,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	composeSvc := compose.NewService(svc)

	attachCtx, cancelAttach := context.WithCancel(cmd.Context())
	defer cancelAttach()

	// Divert the root SIGINT/SIGTERM handler while attached so those signals
	// forward to the remote command, then restore it on detach.
	pauseSignals, resumeSignals := attachSignalHooks(cmd.Context())

	stdin, stdout, stopStdio := attachStdio(attachCtx)
	defer stopStdio()

	opts := cli.AttachOptions{
		NoStdin:       flags.NoStdin,
		SigProxy:      flags.SigProxy,
		DetachKeys:    flags.DetachKeys,
		PauseSignals:  pauseSignals,
		ResumeSignals: resumeSignals,
		Stdin:         os.Stdin,
		Stdout:        os.Stdout,
		StdinPipe:     stdin,
		StdoutPipe:    stdout,
	}

	id, err := composeSvc.ResolveCommandID(attachCtx, selection, serviceName, scaleIndex)
	if err != nil {
		return err
	}

	if flags.AutoExit {
		err := cli.AttachCommand(attachCtx, svc, id, opts)
		if errors.Is(err, cli.ErrRemoteEOF) {
			return nil
		}
		return err
	}
	return cli.AttachCommandSticky(attachCtx, svc, id, opts)
}

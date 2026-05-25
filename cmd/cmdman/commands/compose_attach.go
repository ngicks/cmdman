package commands

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmd/internal/cmdsignals"
	"github.com/ngicks/cmdman/cmd/internal/stdiopipe"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeAttachCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var flags attachFlags

	cmd := &cobra.Command{
		Use:   "attach [flags] SERVICE",
		Short: "Attach to a running compose command's PTY",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeAttach(cmd, rootCfg, cf, args[0], flags)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&flags.NoStdin, "no-stdin", false, "Output-only mode")
	f.BoolVar(&flags.SigProxy, "sig-proxy", true, "Forward signals to command")
	f.StringVar(&flags.DetachKeys, "detach-keys", "ctrl-p,ctrl-q", "Key sequence to detach")

	parent.AddCommand(cmd)
}

func runComposeAttach(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	serviceName string,
	flags attachFlags,
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

	attachCtx, cancelAttach := context.WithCancel(cmd.Context())
	defer cancelAttach()

	session, err := compose.NewService(svc).OpenAttachSession(attachCtx, selection, serviceName)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	return cli.Attach(attachCtx, session, cli.AttachOptions{
		NoStdin:      flags.NoStdin,
		SigProxy:     flags.SigProxy,
		DetachKeys:   flags.DetachKeys,
		ResetSignals: cmdsignals.ExitSignals[:],
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		StdinPipe:    stdiopipe.Stdin(attachCtx),
		StdoutPipe:   stdiopipe.Stdout(attachCtx),
	})
}

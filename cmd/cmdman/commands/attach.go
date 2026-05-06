package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmd/internal/cmdsignals"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

type attachFlags struct {
	NoStdin    bool
	SigProxy   bool
	DetachKeys string
}

func attachCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flags attachFlags

	cmd := &cobra.Command{
		Use:   "attach [flags] ID|NAME",
		Short: "Attach to a running command's PTY",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd, args, rootCfg, flags)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&flags.NoStdin, "no-stdin", false, "Output-only mode")
	f.BoolVar(&flags.SigProxy, "sig-proxy", true, "Forward signals to command")
	f.StringVar(&flags.DetachKeys, "detach-keys", "ctrl-p,ctrl-q", "Key sequence to detach")

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

	ctx := cmd.Context()
	session, err := svc.OpenAttachSession(ctx, args[0])
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	return cli.Attach(ctx, session, cli.AttachOptions{
		NoStdin:      flags.NoStdin,
		SigProxy:     flags.SigProxy,
		DetachKeys:   flags.DetachKeys,
		ResetSignals: cmdsignals.ExitSignals[:],
	})
}

package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func runCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flags      createFlags
		flagAttach bool
	)

	cmd := &cobra.Command{
		Use:   "run [flags] -- COMMAND [ARGS...]",
		Short: "Create and start a new command",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd, args, rootCfg, &flags, flagAttach)
		},
	}

	bindCreateFlags(cmd, &flags)
	cmd.Flags().BoolVar(&flagAttach, "attach", false, "Attach after the command reaches running")

	parent.AddCommand(cmd)
}

func runRun(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	flags *createFlags,
	attach bool,
) error {
	id, name, err := doCreate(cmd, args, rootCfg, flags)
	if err != nil {
		return err
	}

	if err := doStart(cmd, id, rootCfg); err != nil {
		return err
	}

	if !attach {
		displayName := id
		if name != "" {
			displayName = name
		}
		fmt.Fprintln(cmd.OutOrStdout(), displayName)
	} else {
		svc, err := cmdmanService(rootCfg)
		if err != nil {
			return err
		}
		defer svc.Close()

		endpoint, err := svc.ResolveMonitor(cmd.Context(), id)
		if err != nil {
			return err
		}
		if endpoint.SocketPath != "" {
			return runAttach(cmd, []string{id}, rootCfg, attachFlags{
				DetachKeys: "ctrl-p,ctrl-q",
				SigProxy:   true,
			})
		}
	}

	return nil
}

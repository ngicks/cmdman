package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func runCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		flags      createFlags
		flagAttach bool
	)

	cmd := &cobra.Command{
		Use:   "run [flags] -- COMMAND [ARGS...]",
		Short: "Create and start a new command",
		Args:  cobra.MinimumNArgs(1),
		// Positional args are an executable and its arguments; the shell's
		// default file completion is the right behavior, so ValidArgsFunction
		// is intentionally left unset.
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd, args, rf, &flags, flagAttach)
		},
	}

	bindCreateFlags(cmd, &flags)
	cmd.Flags().BoolVar(&flagAttach, "attach", false, "Attach after the command reaches running")

	parent.AddCommand(cmd)
}

func runRun(
	cmd *cobra.Command,
	args []string,
	rf *rootFlags,
	flags *createFlags,
	attach bool,
) error {
	id, name, err := doCreate(cmd, args, rf, flags)
	if err != nil {
		return err
	}

	if err := doStart(cmd, id, rf); err != nil {
		return err
	}

	if !attach {
		displayName := id
		if name != "" {
			displayName = name
		}
		fmt.Fprintln(cmd.OutOrStdout(), displayName)
	} else {
		svc, err := cmdmanService(cmd, rf)
		if err != nil {
			return err
		}
		defer svc.Close()

		status, err := svc.Inspect(cmd.Context(), id)
		if err != nil {
			return err
		}
		if status.StateJSON.SocketPath != "" {
			return runAttach(cmd, []string{id}, rf, attachFlags{
				DetachKeys: "ctrl-p,ctrl-q",
				SigProxy:   true,
			})
		}
	}

	return nil
}

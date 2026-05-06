package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

func signalCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flagSignal string

	cmd := &cobra.Command{
		Use:   "signal -s SIGNAL ID|NAME [ID|NAME...]",
		Short: "Send a raw signal to a running command",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSignal(cmd, args, rootCfg, flagSignal)
		},
	}

	cmd.Flags().StringVarP(&flagSignal, "signal", "s", "", "Signal to send")
	_ = cmd.MarkFlagRequired("signal")

	parent.AddCommand(cmd)
}

func runSignal(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	sigName string,
) error {
	sig, _, err := store.ParseSignal(sigName)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}

	for _, target := range args {
		if err := svc.Signal(cmd.Context(), target, sig); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "signal %s: %v\n", target, err)
		}
	}
	return nil
}

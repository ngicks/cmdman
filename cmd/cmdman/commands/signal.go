package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/hrstr"
)

func signalCmd(parent *cobra.Command, rf *rootFlags) {
	var flagSignal string

	cmd := &cobra.Command{
		Use:               "signal -s SIGNAL ID|NAME [ID|NAME...]",
		Short:             "Send a raw signal to a running command",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeCommandNames(rf, runningStates...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSignal(cmd, args, rf, flagSignal)
		},
	}

	cmd.Flags().StringVarP(&flagSignal, "signal", "s", "", "Signal to send")
	_ = cmd.MarkFlagRequired("signal")
	_ = cmd.RegisterFlagCompletionFunc("signal", signalCompletions)

	parent.AddCommand(cmd)
}

func runSignal(
	cmd *cobra.Command,
	args []string,
	rf *rootFlags,
	sigName string,
) error {
	sig, _, err := hrstr.ParseSignal(sigName)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	for _, target := range args {
		if err := svc.Signal(cmd.Context(), target, sig); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "signal %s: %v\n", target, err)
		}
	}
	return nil
}

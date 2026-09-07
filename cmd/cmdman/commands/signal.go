package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/hrstr"
)

func signalCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		flagSignal       string
		flagIgnoreErrors bool
	)

	cmd := &cobra.Command{
		Use:               "signal -s SIGNAL ID|NAME [ID|NAME...]",
		Short:             "Send a raw signal to a running command",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeCommandNames(rf, runningStates...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSignal(cmd, args, rf, flagSignal, flagIgnoreErrors)
		},
	}

	cmd.Flags().StringVarP(&flagSignal, "signal", "s", "", "Signal to send")
	cmd.Flags().BoolVar(&flagIgnoreErrors, "ignore-errors", false,
		"Exit 0 even when some targets failed; failures are still printed")
	_ = cmd.MarkFlagRequired("signal")
	_ = cmd.RegisterFlagCompletionFunc("signal", signalCompletions)

	parent.AddCommand(cmd)
}

func runSignal(
	cmd *cobra.Command,
	args []string,
	rf *rootFlags,
	sigName string,
	ignoreErrors bool,
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

	return reportTargetErrors(cmd.ErrOrStderr(), "signal", ignoreErrors,
		func(yield func(string, error) bool) {
			for _, target := range args {
				if !yield(target, svc.Signal(cmd.Context(), target, sig)) {
					return
				}
			}
		})
}

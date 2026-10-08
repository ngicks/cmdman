package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

func stopCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		flagSignal       string
		flagTimeout      string
		flagIgnoreErrors bool
	)

	cmd := &cobra.Command{
		Use:               "stop [flags] ID|NAME [ID|NAME...]",
		Short:             "Gracefully stop a running command",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeCommandNames(rf, runningStates...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(cmd, args, rf, flagSignal, flagTimeout, flagIgnoreErrors)
		},
	}

	cmd.Flags().
		StringVarP(&flagSignal, "signal", "s", "", "Signal to send before waiting for shutdown")
	addStopTimeoutFlag(cmd, &flagTimeout)
	cmd.Flags().BoolVar(&flagIgnoreErrors, "ignore-errors", false,
		"Exit 0 even when some targets failed; failures are still printed")
	_ = cmd.RegisterFlagCompletionFunc("signal", signalCompletions)

	parent.AddCommand(cmd)
}

func runStop(
	cmd *cobra.Command,
	args []string,
	rf *rootFlags,
	sigName string,
	timeoutValue string,
	ignoreErrors bool,
) error {
	if sigName != "" {
		if _, _, err := hrstr.ParseSignal(sigName); err != nil {
			return err
		}
	}
	timeout, err := stopTimeoutFlag(cmd, timeoutValue)
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	results, err := svc.Stop(cmd.Context(), cmdman.StopRequest{
		Targets: args,
		Signal:  sigName,
		Timeout: timeout,
	})
	if err != nil {
		return err
	}
	return reportTargetErrors(cmd.ErrOrStderr(), "stop", ignoreErrors,
		func(yield func(string, error) bool) {
			for _, result := range results {
				if !yield(result.ID, result.Err) {
					return
				}
			}
		})
}

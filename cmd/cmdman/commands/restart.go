package commands

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

func restartCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		flagSignal       string
		flagTimeout      int
		flagIgnoreErrors bool
	)

	cmd := &cobra.Command{
		Use:               "restart [flags] ID|NAME [ID|NAME...]",
		Short:             "Stop and start commands (alias of stop followed by start)",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeCommandNames(rf, runningStates...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestart(cmd, args, rf, flagSignal, flagTimeout, flagIgnoreErrors)
		},
	}

	cmd.Flags().
		StringVarP(&flagSignal, "signal", "s", "", "Signal to send before waiting for shutdown")
	cmd.Flags().IntVarP(&flagTimeout, "timeout", "t", 10, "Seconds to wait before sending SIGKILL")
	cmd.Flags().BoolVar(&flagIgnoreErrors, "ignore-errors", false,
		"Exit 0 even when some targets failed; failures are still printed")
	_ = cmd.RegisterFlagCompletionFunc("signal", signalCompletions)

	parent.AddCommand(cmd)
}

func runRestart(
	cmd *cobra.Command,
	args []string,
	rf *rootFlags,
	sigName string,
	timeoutSeconds int,
	ignoreErrors bool,
) error {
	if sigName != "" {
		if _, _, err := hrstr.ParseSignal(sigName); err != nil {
			return err
		}
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	results, err := svc.Restart(cmd.Context(), cmdman.RestartRequest{
		Targets: args,
		Signal:  sigName,
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	})
	if err != nil {
		return err
	}
	return reportTargetErrors(cmd.ErrOrStderr(), "restart", ignoreErrors,
		func(yield func(string, error) bool) {
			for _, result := range results {
				if !yield(result.ID, result.Err) {
					return
				}
			}
		})
}

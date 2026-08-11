package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

func stopCmd(parent *cobra.Command, rf *rootFlags) {
	var (
		flagSignal  string
		flagTimeout int
	)

	cmd := &cobra.Command{
		Use:               "stop [flags] ID|NAME [ID|NAME...]",
		Short:             "Gracefully stop a running command",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeCommandNames(rf, runningStates...),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStop(cmd, args, rf, flagSignal, flagTimeout)
		},
	}

	cmd.Flags().
		StringVarP(&flagSignal, "signal", "s", "", "Signal to send before waiting for shutdown")
	cmd.Flags().IntVarP(&flagTimeout, "timeout", "t", 10, "Seconds to wait before sending SIGKILL")
	_ = cmd.RegisterFlagCompletionFunc("signal", signalCompletions)

	parent.AddCommand(cmd)
}

func runStop(
	cmd *cobra.Command,
	args []string,
	rf *rootFlags,
	sigName string,
	timeoutSeconds int,
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

	results, err := svc.Stop(cmd.Context(), cmdman.StopRequest{
		Targets: args,
		Signal:  sigName,
		Timeout: time.Duration(timeoutSeconds) * time.Second,
	})
	if err != nil {
		return err
	}
	for _, result := range results {
		if result.Err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "stop %s: %v\n", result.ID, result.Err)
		}
	}
	return nil
}

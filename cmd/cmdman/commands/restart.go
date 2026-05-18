package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

func restartCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagSignal  string
		flagTimeout int
	)

	cmd := &cobra.Command{
		Use:   "restart [flags] ID|NAME [ID|NAME...]",
		Short: "Stop and start commands (alias of stop followed by start)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestart(cmd, args, rootCfg, flagSignal, flagTimeout)
		},
	}

	cmd.Flags().
		StringVarP(&flagSignal, "signal", "s", "", "Signal to send before waiting for shutdown")
	cmd.Flags().IntVarP(&flagTimeout, "timeout", "t", 10, "Seconds to wait before sending SIGKILL")

	parent.AddCommand(cmd)
}

func runRestart(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	sigName string,
	timeoutSeconds int,
) error {
	if sigName != "" {
		if _, _, err := store.ParseSignal(sigName); err != nil {
			return err
		}
	}

	svc, err := cmdmanService(rootCfg)
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
	for _, result := range results {
		if result.Err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "restart %s: %v\n", result.ID, result.Err)
		}
	}
	return nil
}

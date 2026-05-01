package commands

import (
	"fmt"
	"time"

	"github.com/ngicks/crabswarm/pkg/cmdman"
	"github.com/ngicks/crabswarm/pkg/cmdman/store"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(stopCmd)
	stopCmd.Flags().StringP("signal", "s", "", "Signal to send before waiting for shutdown")
	stopCmd.Flags().IntP("timeout", "t", 10, "Seconds to wait before sending SIGKILL")
}

var stopCmd = &cobra.Command{
	Use:   "stop [flags] ID|NAME [ID|NAME...]",
	Short: "Gracefully stop a running command",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runStop,
}

func runStop(cmd *cobra.Command, args []string) error {
	sigName, _ := cmd.Flags().GetString("signal")
	timeoutSeconds, _ := cmd.Flags().GetInt("timeout")
	if sigName != "" {
		if _, _, err := store.ParseSignal(sigName); err != nil {
			return err
		}
	}

	svc, err := cmdmanService()
	if err != nil {
		return err
	}

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

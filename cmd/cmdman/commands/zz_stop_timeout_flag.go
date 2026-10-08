package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/hrstr"
)

// stopTimeoutFlagName is the -t/--timeout flag of the verbs that stop commands.
const stopTimeoutFlagName = "timeout"

// addStopTimeoutFlag registers -t/--timeout on cmd, bound to timeout.
func addStopTimeoutFlag(cmd *cobra.Command, timeout *string) {
	cmd.Flags().StringVarP(timeout, stopTimeoutFlagName, "t", "",
		"Time to wait after the stop signal before sending SIGKILL, as integer seconds"+
			" or a duration like 1m30s (default: the command's stored stop timeout, else 10s)")
}

// stopTimeoutFlag returns the -t/--timeout value cmd was given, nil when the
// flag is unset so the stop falls back to each command's stored stop timeout.
func stopTimeoutFlag(cmd *cobra.Command, value string) (*time.Duration, error) {
	if !cmd.Flags().Changed(stopTimeoutFlagName) {
		return nil, nil
	}
	d, err := hrstr.ParseTimeout(value)
	if err != nil {
		return nil, fmt.Errorf("--%s: %w", stopTimeoutFlagName, err)
	}
	return &d, nil
}

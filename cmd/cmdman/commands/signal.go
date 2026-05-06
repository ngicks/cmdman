package commands

import (
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(signalCmd)
	signalCmd.Flags().StringP("signal", "s", "", "Signal to send")
	_ = signalCmd.MarkFlagRequired("signal")
}

var signalCmd = &cobra.Command{
	Use:   "signal -s SIGNAL ID|NAME [ID|NAME...]",
	Short: "Send a raw signal to a running command",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runSignal,
}

func runSignal(cmd *cobra.Command, args []string) error {
	sigName, _ := cmd.Flags().GetString("signal")
	sig, _, err := store.ParseSignal(sigName)
	if err != nil {
		return err
	}

	svc, err := cmdmanService()
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

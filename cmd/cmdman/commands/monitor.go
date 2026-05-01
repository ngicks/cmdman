package commands

import (
	"log/slog"
	"os"

	"github.com/ngicks/crabswarm/pkg/cmdman"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(monitorCmd)
	monitorCmd.Flags().String("id", "", "Command ID")
	monitorCmd.MarkFlagRequired("id")
}

var monitorCmd = &cobra.Command{
	Use:    "__monitor",
	Short:  "Internal monitor process (do not call directly)",
	Hidden: true,
	RunE:   runMonitor,
}

func runMonitor(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetString("id")
	cfg, err := cmdmanConfig()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}))

	return cmdman.RunMonitor(cmd.Context(), id, cfg, logger)
}

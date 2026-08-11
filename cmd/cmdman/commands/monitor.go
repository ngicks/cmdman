package commands

import (
	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/monitor"
)

func monitorCmd(parent *cobra.Command, rf *rootFlags) {
	var flagID string

	cmd := &cobra.Command{
		Use:               "__monitor",
		Short:             "Internal monitor process (do not call directly)",
		Hidden:            true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMonitor(cmd, args, rf, flagID)
		},
	}

	cmd.Flags().StringVar(&flagID, "id", "", "Command ID")
	_ = cmd.MarkFlagRequired("id")

	parent.AddCommand(cmd)
}

func runMonitor(cmd *cobra.Command, _ []string, rf *rootFlags, id string) error {
	cfg, err := loadConfig(cmd, rf)
	if err != nil {
		return err
	}
	logger := contextkey.ValueSlogLoggerDefault(cmd.Context())
	return monitor.DaemonizeMonitor(cmd.Context(), id, cfg, logger)
}

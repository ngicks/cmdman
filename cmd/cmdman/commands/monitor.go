package commands

import (
	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func monitorCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flagID string

	cmd := &cobra.Command{
		Use:               "__monitor",
		Short:             "Internal monitor process (do not call directly)",
		Hidden:            true,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMonitor(cmd, args, rootCfg, flagID)
		},
	}

	cmd.Flags().StringVar(&flagID, "id", "", "Command ID")
	_ = cmd.MarkFlagRequired("id")

	parent.AddCommand(cmd)
}

func runMonitor(cmd *cobra.Command, _ []string, rootCfg *cmdman.CmdmanConfig, id string) error {
	cfg, err := rootCfg.WithDefaults()
	if err != nil {
		return err
	}
	logger := contextkey.ValueSlogLoggerDefault(cmd.Context())
	return cmdman.DaemonizeMonitor(cmd.Context(), id, cfg, logger)
}

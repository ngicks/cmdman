package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func logsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flagFollow bool

	cmd := &cobra.Command{
		Use:   "logs [flags] ID|NAME",
		Short: "Show command output from the on-disk log file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, args, rootCfg, flagFollow)
		},
	}

	cmd.Flags().BoolVarP(&flagFollow, "follow", "f", false, "Follow output")

	parent.AddCommand(cmd)
}

func runLogs(cmd *cobra.Command, args []string, rootCfg *cmdman.CmdmanConfig, follow bool) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	return svc.Logs(cmd.Context(), cmdman.LogsRequest{
		IDOrName: args[0],
		Follow:   follow,
		Writer:   cmd.OutOrStdout(),
	})
}

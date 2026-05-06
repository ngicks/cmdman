package commands

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
)

func inspectCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	cmd := &cobra.Command{
		Use:   "inspect ID|NAME",
		Short: "Show merged command definition, runtime state, and exit history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInspect(cmd, args, rootCfg)
		},
	}

	parent.AddCommand(cmd)
}

func runInspect(cmd *cobra.Command, args []string, rootCfg *cmdman.CmdmanConfig) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	out, err := svc.Inspect(cmd.Context(), args[0])
	if err != nil {
		return err
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

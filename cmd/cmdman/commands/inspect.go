package commands

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(inspectCmd)
}

var inspectCmd = &cobra.Command{
	Use:   "inspect ID|NAME",
	Short: "Show merged command definition, runtime state, and exit history",
	Args:  cobra.ExactArgs(1),
	RunE:  runInspect,
}

func runInspect(cmd *cobra.Command, args []string) error {
	svc, err := cmdmanService()
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

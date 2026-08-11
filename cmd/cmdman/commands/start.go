package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/model"
)

func startCmd(parent *cobra.Command, rf *rootFlags) {
	cmd := &cobra.Command{
		Use:               "start ID_OR_NAME",
		Short:             "Start a created command",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCommandNames(rf, model.EventTypeCreated),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, args, rf)
		},
	}

	parent.AddCommand(cmd)
}

func runStart(cmd *cobra.Command, args []string, rf *rootFlags) error {
	return doStart(cmd, args[0], rf)
}

func doStart(cmd *cobra.Command, idOrName string, rf *rootFlags) error {
	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	return svc.Start(cmd.Context(), idOrName)
}

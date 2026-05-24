package commands

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

func composeWaitCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagCondition string
		flagInterval  time.Duration
		flagIgnore    bool
	)

	cmd := &cobra.Command{
		Use:   "wait [COMMAND...]",
		Short: "Wait for compose commands to reach a condition",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeWait(cmd, rootCfg, cf, args, flagCondition, flagInterval, flagIgnore)
		},
	}

	cmd.Flags().StringVar(&flagCondition, "condition", "",
		`Wait condition: stopped (default), created, starting, started, exited, failed`)
	cmd.Flags().DurationVar(&flagInterval, "interval", 0,
		"Polling interval (default: 250ms)")
	cmd.Flags().BoolVar(&flagIgnore, "ignore", false,
		"Ignore commands that cannot be resolved")

	parent.AddCommand(cmd)
}

func runComposeWait(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	condition string,
	interval time.Duration,
	ignore bool,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	result, err := compose.NewService(svc).Wait(cmd.Context(), selection, compose.WaitOption{
		CommandNames: commandNames,
		Condition:    model.EventType(condition),
		Interval:     interval,
		Ignore:       ignore,
	})
	if err != nil {
		return err
	}

	return cli.PrintWaitResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Outcomes)
}

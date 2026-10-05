package commands

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/model"
)

func composeWaitCmd(parent *cobra.Command, rf *rootFlags, cf *composeFlags) {
	var (
		flagCondition string
		flagInterval  time.Duration
		flagIgnore    bool
		flagScale     int
	)

	cmd := &cobra.Command{
		Use:               "wait [COMMAND...]",
		Short:             "Wait for compose commands to reach a condition",
		Args:              scaleArgs(cobra.ArbitraryArgs, &flagScale),
		ValidArgsFunction: completeComposeCommands(rf, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeWait(
				cmd, rf, cf, args, flagScale, flagCondition, flagInterval, flagIgnore,
			)
		},
	}

	cmd.Flags().StringVar(&flagCondition, "condition", "",
		`Wait condition: stopped (default), created, starting, running, exited, failed`)
	cmd.Flags().DurationVar(&flagInterval, "interval", 0,
		"Polling interval (default: 250ms)")
	cmd.Flags().BoolVar(&flagIgnore, "ignore", false,
		"Ignore commands that cannot be resolved")
	addScaleFlag(cmd, &flagScale)
	_ = cmd.RegisterFlagCompletionFunc("condition", waitConditionCompletions)

	parent.AddCommand(cmd)
}

func runComposeWait(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	commandNames []string,
	scale int,
	condition string,
	interval time.Duration,
	ignore bool,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(cmd, rf)
	if err != nil {
		return err
	}
	defer svc.Close()

	result, err := compose.NewService(svc).Wait(cmd.Context(), selection, compose.WaitOption{
		Targets:   composeTargets(commandNames, scale),
		Condition: model.EventType(condition),
		Interval:  interval,
		Ignore:    ignore,
	})
	if err != nil {
		return err
	}

	return cli.PrintWaitResult(cmd.OutOrStdout(), cmd.ErrOrStderr(), result.Outcomes)
}

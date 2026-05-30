package commands

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

func waitCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagCondition string
		flagInterval  time.Duration
		flagIgnore    bool
	)

	cmd := &cobra.Command{
		Use:               "wait [flags] ID|NAME [ID|NAME...]",
		Short:             "Block until one or more commands stop, then print exit codes",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeCommandNames(rootCfg),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWait(cmd, args, rootCfg, flagCondition, flagInterval, flagIgnore)
		},
	}

	cmd.Flags().StringVarP(&flagCondition, "condition", "c", string(cmdman.WaitConditionStopped),
		"Condition to wait on (stopped|created|starting|started|exited|failed)")
	cmd.Flags().DurationVarP(&flagInterval, "interval", "i", 250*time.Millisecond,
		"Time interval between state checks")
	cmd.Flags().BoolVar(&flagIgnore, "ignore", false,
		"Don't fail on missing command errors")
	_ = cmd.RegisterFlagCompletionFunc("condition", waitConditionCompletions)

	parent.AddCommand(cmd)
}

func runWait(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	condition string,
	interval time.Duration,
	ignore bool,
) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	results, err := svc.Wait(cmd.Context(), cmdman.WaitRequest{
		Targets:   args,
		Condition: model.EventType(condition),
		Interval:  interval,
		Ignore:    ignore,
	})
	if err != nil {
		return err
	}

	var hadErr bool
	for _, result := range results {
		if result.Err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "wait %s: %v\n", result.ID, result.Err)
			hadErr = true
			continue
		}
		if result.ExitCode != nil {
			fmt.Fprintln(cmd.OutOrStdout(), *result.ExitCode)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), 0)
		}
	}
	if hadErr {
		return errors.New("one or more wait operations failed")
	}
	return nil
}

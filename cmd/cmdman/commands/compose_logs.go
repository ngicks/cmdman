package commands

import (
	"context"
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeLogsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagFollow bool
		flagSince  string
		flagUntil  string
		flagHead   int
		flagTail   int
	)

	cmd := &cobra.Command{
		Use:               "logs [COMMAND...]",
		Short:             "Fetch logs from compose commands",
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeComposeCommands(rootCfg, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeLogs(
				cmd, rootCfg, cf, args,
				flagFollow, flagSince, flagUntil, flagHead, flagTail,
			)
		},
	}

	cmd.Flags().BoolVar(&flagFollow, "follow", false, "Follow log output")
	cmd.Flags().StringVar(&flagSince, "since", "", "Show logs since timestamp (RFC3339)")
	cmd.Flags().StringVar(&flagUntil, "until", "", "Show logs until timestamp (RFC3339)")
	cmd.Flags().
		IntVar(&flagHead, "head", 0, "Return only the first N records per command (0 = no limit)")
	cmd.Flags().
		IntVar(&flagTail, "tail", 0, "Return only the last N records per command (0 = no limit)")

	parent.AddCommand(cmd)
}

func runComposeLogs(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	follow bool,
	sinceStr, untilStr string,
	head, tail int,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

	var since, until time.Time
	if sinceStr != "" {
		since, err = time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			return err
		}
	}
	if untilStr != "" {
		until, err = time.Parse(time.RFC3339, untilStr)
		if err != nil {
			return err
		}
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	msgs, errc := compose.NewService(svc).Logs(ctx, selection, compose.LogsOption{
		CommandNames: commandNames,
		Follow:       follow,
		Since:        since,
		Until:        until,
		Head:         head,
		Tail:         tail,
	})

	writeErr := cli.PrintComposeLogs(cmd.OutOrStdout(), cmd.ErrOrStderr(), msgs)
	if writeErr != nil {
		// Unblock producers that may be parked on a send so errc can resolve.
		cancel()
	}
	return errors.Join(writeErr, <-errc)
}

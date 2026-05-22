package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmd/internal/stdiopipe"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

func logsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagFollow bool
		flagSince  string
		flagUntil  string
		flagHead   int
		flagTail   int
	)

	cmd := &cobra.Command{
		Use:   "logs [flags] ID|NAME",
		Short: "Show command output from the on-disk log file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, args, rootCfg, flagFollow, flagSince, flagUntil, flagHead, flagTail)
		},
	}

	cmd.Flags().BoolVarP(&flagFollow, "follow", "f", false, "Follow output")
	cmd.Flags().StringVar(
		&flagSince,
		"since",
		"",
		`Show logs since timestamp ("now" or RFC3339; e.g. 2026-01-02T15:04:05Z)`,
	)
	cmd.Flags().StringVar(&flagUntil, "until", "", `Show logs until timestamp ("now" or RFC3339)`)
	cmd.Flags().IntVar(&flagHead, "head", 0, "Show at most N first log lines (0 = unlimited)")
	cmd.Flags().IntVar(&flagTail, "tail", 0, "Show at most N last log lines (0 = unlimited)")

	parent.AddCommand(cmd)
}

func runLogs(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	follow bool,
	sinceFlag string,
	untilFlag string,
	head int,
	tail int,
) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	since, err := hrstr.ParseTime(sinceFlag, time.Now)
	if err != nil {
		return fmt.Errorf("parse --since: %w", err)
	}
	until, err := hrstr.ParseTime(untilFlag, time.Now)
	if err != nil {
		return fmt.Errorf("parse --until: %w", err)
	}

	r, err := svc.Logs(cmd.Context(), cmdman.LogsRequest{
		IDOrName: args[0],
		Follow:   follow,
		Since:    since,
		Until:    until,
		Head:     head,
		Tail:     tail,
	})
	if err != nil {
		return err
	}
	defer r.Close()

	stdout := stdiopipe.Stdout(cmd.Context())
	defer stdout.Close()
	stderr := stdiopipe.Stderr(cmd.Context())
	defer stderr.Close()

	return cli.RenderLogs(stdout, stderr, r.Records())
}

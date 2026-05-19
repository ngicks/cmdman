package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmd/internal/stdiopipe"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/eventlog"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

func eventsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagFollow  bool
		flagSince   string
		flagUntil   string
		flagFromEnd bool
		flagIDs     []string
		flagTypes   []string
		flagFormat  string
	)

	cmd := &cobra.Command{
		Use:   "events [flags]",
		Short: "Stream events from the on-disk event log",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEvents(
				cmd, args, rootCfg,
				flagFollow, flagSince, flagUntil, flagFromEnd,
				flagIDs, flagTypes, flagFormat,
			)
		},
	}

	cmd.Flags().
		BoolVarP(&flagFollow, "follow", "f", false, "Follow new events as they are appended")
	cmd.Flags().StringVar(&flagSince, "since", "",
		`Show events since timestamp ("now" or RFC3339)`)
	cmd.Flags().StringVar(&flagUntil, "until", "",
		`Show events until timestamp ("now" or RFC3339)`)
	cmd.Flags().BoolVar(&flagFromEnd, "from-end", false,
		"Skip existing entries; only deliver new ones (implies --follow)")
	cmd.Flags().StringSliceVar(&flagIDs, "id", nil,
		"Filter by command ID (repeatable)")
	cmd.Flags().StringSliceVar(&flagTypes, "type", nil,
		"Filter by event type (repeatable)")
	cmd.Flags().StringVar(&flagFormat, "format", "",
		cli.EventsFormatUsage())

	parent.AddCommand(cmd)
}

func runEvents(
	cmd *cobra.Command,
	_ []string,
	rootCfg *cmdman.CmdmanConfig,
	follow bool,
	sinceFlag string,
	untilFlag string,
	fromEnd bool,
	ids []string,
	types []string,
	format string,
) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	since, err := logdriver.ParseLogTime(sinceFlag, time.Now)
	if err != nil {
		return fmt.Errorf("parse --since: %w", err)
	}
	until, err := logdriver.ParseLogTime(untilFlag, time.Now)
	if err != nil {
		return fmt.Errorf("parse --until: %w", err)
	}

	typeFilter := make([]eventlog.EventType, 0, len(types))
	for _, t := range types {
		if t == "" {
			continue
		}
		if !eventlog.IsEventType(t) {
			return fmt.Errorf("--type: unknown event type %q", t)
		}
		typeFilter = append(typeFilter, eventlog.EventType(t))
	}

	sub, err := svc.Events(cmd.Context(), cmdman.EventsRequest{
		Follow:     follow,
		Since:      since,
		Until:      until,
		FromEnd:    fromEnd,
		IDFilter:   ids,
		TypeFilter: typeFilter,
	})
	if err != nil {
		return err
	}
	defer sub.Close()

	stdout := stdiopipe.Stdout(cmd.Context())
	defer stdout.Close()

	return cli.RenderEvents(stdout, sub.Records(), format)
}

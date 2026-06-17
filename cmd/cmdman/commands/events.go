package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/internal/stdiopipe"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

func eventsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flagNoFollow bool
		flagSince    string
		flagUntil    string
		flagIDs      []string
		flagTypes    []string
		flagFormat   string
	)

	cmd := &cobra.Command{
		Use:               "events [flags]",
		Short:             "Stream events from the on-disk event log",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEvents(
				cmd, args, rootCfg,
				flagNoFollow, flagSince, flagUntil,
				flagIDs, flagTypes, flagFormat,
			)
		},
	}

	cmd.Flags().BoolVar(&flagNoFollow, "no-follow", false,
		"Read existing entries and exit instead of tailing new events")
	cmd.Flags().StringVar(&flagSince, "since", "",
		`Show events since timestamp ("now", RFC3339, or a Go duration offset`+
			` from now like -5m); when tailing, omitting both --since and`+
			` --until skips historical entries`)
	cmd.Flags().StringVar(&flagUntil, "until", "",
		`Show events until timestamp ("now", RFC3339, or a Go duration offset`+
			` from now like -5m)`)
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
	noFollow bool,
	sinceFlag string,
	untilFlag string,
	ids []string,
	types []string,
	format string,
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

	typeFilter := make([]model.EventType, 0, len(types))
	for _, t := range types {
		if t == "" {
			continue
		}
		if !model.IsEventType(t) {
			return fmt.Errorf("--type: unknown event type %q", t)
		}
		typeFilter = append(typeFilter, model.EventType(t))
	}

	sub, err := svc.Events(cmd.Context(), cmdman.EventsRequest{
		NoFollow:   noFollow,
		Since:      since,
		Until:      until,
		IDFilter:   ids,
		TypeFilter: typeFilter,
	})
	if err != nil {
		return err
	}

	stdout := stdiopipe.Stdout(cmd.Context())
	defer stdout.Close()

	// Render until the records channel is closed (which happens when the
	// underlying Reader.Run / Watcher.Run goroutines exit), then surface
	// any error captured by the subscription's errgroup. Without this, a
	// watcher setup failure inside Run would close the channel with no
	// Record.Err, masquerading as a clean exit.
	renderErr := cli.RenderEvents(stdout, sub.Records(), format)
	closeErr := sub.Close()
	if renderErr != nil {
		return renderErr
	}
	return closeErr
}

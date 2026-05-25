package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmd/internal/stdiopipe"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

func composeEventsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagNoFollow bool
		flagSince    string
		flagUntil    string
		flagTypes    []string
		flagFormat   string
	)

	cmd := &cobra.Command{
		Use:   "events [COMMAND...]",
		Short: "Stream events for compose commands",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeEvents(
				cmd, rootCfg, cf, args,
				flagNoFollow, flagSince, flagUntil, flagTypes, flagFormat,
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
	cmd.Flags().StringSliceVar(&flagTypes, "type", nil,
		"Filter by event type (repeatable)")
	cmd.Flags().StringVar(&flagFormat, "format", "", cli.EventsFormatUsage())

	parent.AddCommand(cmd)
}

func runComposeEvents(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	commandNames []string,
	noFollow bool,
	sinceFlag string,
	untilFlag string,
	types []string,
	format string,
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}

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

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	sub, err := compose.NewService(svc).Events(cmd.Context(), selection, compose.EventsOption{
		CommandNames: commandNames,
		NoFollow:     noFollow,
		Since:        since,
		Until:        until,
		Types:        typeFilter,
	})
	if err != nil {
		return err
	}
	if sub == nil {
		// No commands in the project; nothing to stream.
		return nil
	}

	stdout := stdiopipe.Stdout(cmd.Context())
	defer stdout.Close()

	renderErr := cli.RenderEvents(stdout, sub.Records(), format)
	closeErr := sub.Close()
	if renderErr != nil {
		return renderErr
	}
	return closeErr
}

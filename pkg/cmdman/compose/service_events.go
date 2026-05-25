package compose

import (
	"context"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// EventsOption configures a compose Events subscription.
type EventsOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
	// NoFollow delivers existing entries and exits instead of tailing new events.
	NoFollow bool
	// Since/Until clamp the event time window (see cmdman.EventsRequest).
	Since time.Time
	Until time.Time
	// Types restricts delivery to the given event types. Empty means all types.
	Types []model.EventType
}

// Events subscribes to the event log filtered to the selected project's command
// IDs. With no command-name filter it covers every command in the project;
// names narrow the set.
//
// The project command IDs are resolved up front and set as the subscription's
// ID filter, so only events for project commands are delivered. Per
// resolved-decision 15, when the project has no commands it logs a warning and
// returns a nil subscription; callers should treat nil as "nothing to stream"
// and exit 0.
func (s *Service) Events(
	ctx context.Context,
	selection ProjectSelection,
	opts EventsOption,
) (*cmdman.EventsSubscription, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	if err := validateCommandNames(opts.CommandNames, selection.Spec, entries); err != nil {
		return nil, err
	}
	if len(opts.CommandNames) > 0 {
		entries = filterByCommandNames(entries, opts.CommandNames)
	}

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose events: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "events",
		)
		return nil, nil
	}

	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}

	return s.svc.Events(ctx, cmdman.EventsRequest{
		NoFollow:   opts.NoFollow,
		Since:      opts.Since,
		Until:      opts.Until,
		IDFilter:   ids,
		TypeFilter: opts.Types,
	})
}

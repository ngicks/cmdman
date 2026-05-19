package cmdman

import (
	"context"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/eventlog"
)

// EventsRequest defines an event-log subscription.
type EventsRequest struct {
	// Follow keeps the subscription open after the existing tail is
	// drained.
	Follow bool
	// Since/Until clamp the event time window. Until=zero combined with
	// Follow=true tails indefinitely.
	Since time.Time
	Until time.Time
	// FromEnd discards existing entries and only delivers events that
	// arrive after the subscription is established. Implies Follow=true.
	FromEnd bool
	// IDFilter restricts delivery to events whose ID matches any value.
	// Empty/nil means no filter.
	IDFilter []string
	// TypeFilter restricts delivery to events whose Type matches any
	// value. Empty/nil means no filter.
	TypeFilter []eventlog.EventType
}

// EventsSubscription wraps an event reader with its post-filter channel
// so callers can range over events and Close to tear down.
type EventsSubscription struct {
	reader *eventlog.Reader
	out    chan eventlog.Record
	cancel context.CancelFunc
}

// Events constructs a filtered subscription to the event log. The active
// log file lives at the configured EventLogPath and is created lazily by
// whichever process appends first.
func (s *Service) Events(ctx context.Context, req EventsRequest) (*EventsSubscription, error) {
	path, err := s.cfg.EventLogPath()
	if err != nil {
		return nil, err
	}
	follow := req.Follow || req.FromEnd

	subCtx, cancel := context.WithCancel(ctx)
	r, err := eventlog.NewReader(subCtx, path, eventlog.ReaderOption{
		Follow:      follow,
		Since:       req.Since,
		Until:       req.Until,
		FromEnd:     req.FromEnd,
		WatcherKind: s.cfg.EventWatcherKind,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("eventlog: open reader: %w", err)
	}

	idAllow := buildSet(req.IDFilter)
	typeAllow := buildSet(eventTypesAsStrings(req.TypeFilter))

	out := make(chan eventlog.Record, 16)
	sub := &EventsSubscription{reader: r, out: out, cancel: cancel}
	go func() {
		defer close(out)
		for rec := range r.Events() {
			if rec.Err != nil {
				select {
				case <-subCtx.Done():
				case out <- rec:
				}
				continue
			}
			if !matchesFilter(rec.Event, idAllow, typeAllow) {
				continue
			}
			select {
			case <-subCtx.Done():
				return
			case out <- rec:
			}
		}
	}()
	return sub, nil
}

// Records returns the filtered event channel.
func (sub *EventsSubscription) Records() <-chan eventlog.Record {
	return sub.out
}

// Close stops the reader and releases watcher resources.
func (sub *EventsSubscription) Close() error {
	sub.cancel()
	return sub.reader.Close()
}

func buildSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		out[v] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func eventTypesAsStrings(types []eventlog.EventType) []string {
	if len(types) == 0 {
		return nil
	}
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, string(t))
	}
	return out
}

func matchesFilter(e eventlog.Event, idAllow, typeAllow map[string]struct{}) bool {
	if idAllow != nil {
		if _, ok := idAllow[e.ID]; !ok {
			return false
		}
	}
	if typeAllow != nil {
		if _, ok := typeAllow[string(e.Type)]; !ok {
			return false
		}
	}
	return true
}

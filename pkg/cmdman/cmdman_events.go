package cmdman

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman/eventlog"
)

// EventsRequest defines an event-log subscription. The zero value tails
// new events as they are appended (the common case).
type EventsRequest struct {
	// NoFollow disables tailing. The subscription delivers existing
	// entries (read from the start, subject to the Since/Until clamps)
	// and exits instead of waiting for more.
	NoFollow bool
	// Since/Until clamp the event time window. When NoFollow=false
	// (tailing) and BOTH Since and Until are zero, the subscription
	// skips entries already on disk — set either to read history.
	// Until=zero combined with NoFollow=false tails indefinitely.
	Since time.Time
	Until time.Time
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
	out    chan eventlog.Record
	cancel context.CancelFunc
	eg     *errgroup.Group
}

// Events constructs a filtered subscription to the event log. The active
// log file lives at the configured EventLogPath and is created lazily by
// whichever process appends first.
func (s *Service) Events(ctx context.Context, req EventsRequest) (*EventsSubscription, error) {
	if !req.Since.IsZero() && !req.Until.IsZero() && req.Since.After(req.Until) {
		return nil, fmt.Errorf("eventlog: since must not be after until")
	}
	path, err := s.cfg.EventLogPath()
	if err != nil {
		return nil, err
	}
	follow := !req.NoFollow
	// Skip historical entries (seek to EOF) only when the caller is
	// tailing AND has not asked for any time window — otherwise we must
	// read the file from the start so the Since/Until/one-shot semantics
	// produce the expected output. In particular --until alone implies
	// "deliver history up to that point", which requires reading from
	// the start even when tailing.
	fromEnd := follow && req.Since.IsZero() && req.Until.IsZero()

	r, err := eventlog.NewReader(path, eventlog.ReaderOption{
		Follow:      follow,
		Since:       req.Since,
		Until:       req.Until,
		FromEnd:     fromEnd,
		WatcherKind: s.cfg.EventWatcherKind,
	})
	if err != nil {
		return nil, fmt.Errorf("eventlog: open reader: %w", err)
	}

	subCtx, cancel := context.WithCancel(ctx)
	idAllow := buildSet(req.IDFilter)
	typeAllow := buildSet(eventTypesAsStrings(req.TypeFilter))
	out := make(chan eventlog.Record, 16)

	eg, egctx := errgroup.WithContext(subCtx)
	// Run is the producer: it closes r.rec when done. We deliberately do
	// not run a deferred cancel() here — Run returning normally should not
	// preempt the consumer that is still draining r.rec. errgroup auto-
	// cancels egctx when Run returns a non-nil error, which is what we
	// want for the error path.
	eg.Go(func() error {
		return r.Run(egctx)
	})
	eg.Go(func() error {
		defer close(out)
		defer cancel()
		for rec := range r.Events() {
			if rec.Err == nil && !matchesFilter(rec.Event, idAllow, typeAllow) {
				continue
			}
			// Prefer to deliver — the channel is buffered, so a healthy
			// consumer never blocks. Only fall through to the ctx check
			// when the send would block.
			select {
			case out <- rec:
				continue
			default:
			}
			select {
			case out <- rec:
			case <-egctx.Done():
				return nil
			}
		}
		return nil
	})

	return &EventsSubscription{out: out, cancel: cancel, eg: eg}, nil
}

// Records returns the filtered event channel.
func (sub *EventsSubscription) Records() <-chan eventlog.Record {
	return sub.out
}

// Close stops the reader and releases watcher resources.
func (sub *EventsSubscription) Close() error {
	sub.cancel()
	return sub.eg.Wait()
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

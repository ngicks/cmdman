package eventlog

import (
	"context"
	"fmt"
	"time"
)

// WatcherKind selects the change-notification backend used by readers.
type WatcherKind string

const (
	// WatcherKindInotify uses inotify on the parent directory. Linux only.
	WatcherKindInotify WatcherKind = "inotify"
	// WatcherKindPoll uses periodic stat() of the active log path. Portable.
	WatcherKindPoll WatcherKind = "poll"
)

// IsWatcherKind reports whether s is a recognised WatcherKind.
func IsWatcherKind(s string) bool {
	switch WatcherKind(s) {
	case WatcherKindInotify, WatcherKindPoll:
		return true
	}
	return false
}

// DefaultPollInterval is the stat-polling cadence used when no override is
// supplied.
const DefaultPollInterval = 200 * time.Millisecond

// Watcher emits a notification each time the watched file may have new
// content. It is intentionally coarse: a single Events channel that yields
// zero-value tokens. Readers re-read the file on every token and rely on
// EOF to know when to wait again.
type Watcher interface {
	// Events emits a token whenever the active log path may have changed.
	// It is implementation-defined whether tokens are de-bounced; readers
	// must tolerate spurious wake-ups.
	Events() <-chan struct{}
	// Close releases watcher resources. Safe to call multiple times.
	Close() error
}

// NewWatcher builds a Watcher for the given path using the requested kind.
// The poll interval defaults to DefaultPollInterval when zero.
func NewWatcher(
	ctx context.Context,
	kind WatcherKind,
	path string,
	pollInterval time.Duration,
) (Watcher, error) {
	if path == "" {
		return nil, fmt.Errorf("eventlog: watcher path is empty")
	}
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	switch kind {
	case "", WatcherKindPoll:
		return newPollWatcher(ctx, path, pollInterval), nil
	case WatcherKindInotify:
		return newInotifyWatcher(ctx, path)
	default:
		return nil, fmt.Errorf("eventlog: unknown watcher kind %q", kind)
	}
}

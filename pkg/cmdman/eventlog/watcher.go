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
//
// The caller drives the watcher's lifetime: spawn Run in its own goroutine
// and cancel ctx to tear it down. Run acquires any backend resources on
// entry and releases them before returning. Events is closed once Run
// returns.
type Watcher interface {
	// Events emits a token whenever the watched path may have changed.
	// It is implementation-defined whether tokens are de-bounced; readers
	// must tolerate spurious wake-ups. The channel is closed when Run
	// returns.
	Events() <-chan struct{}
	// Run drives the watcher until ctx is cancelled. It must be called
	// exactly once; calling it twice or after a previous Run has returned
	// is a programmer error. A nil return on ctx cancellation is normal;
	// any other error indicates a setup or runtime failure.
	Run(ctx context.Context) error
}

// NewWatcher builds a Watcher for the given path using the requested kind.
// The poll interval defaults to DefaultPollInterval when zero. The returned
// watcher has not started yet — the caller invokes Run on its own goroutine.
func NewWatcher(kind WatcherKind, path string, pollInterval time.Duration) (Watcher, error) {
	if path == "" {
		return nil, fmt.Errorf("eventlog: watcher path is empty")
	}
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	switch kind {
	case "", WatcherKindPoll:
		return newPollWatcher(path, pollInterval), nil
	case WatcherKindInotify:
		return newInotifyWatcher(path)
	default:
		return nil, fmt.Errorf("eventlog: unknown watcher kind %q", kind)
	}
}

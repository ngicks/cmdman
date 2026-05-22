package eventlog

import (
	"context"
	"os"
	"time"
)

type pollWatcher struct {
	events   chan struct{}
	path     string
	interval time.Duration
}

func newPollWatcher(path string, interval time.Duration) *pollWatcher {
	return &pollWatcher{
		events:   make(chan struct{}, 1),
		path:     path,
		interval: interval,
	}
}

func (w *pollWatcher) Events() <-chan struct{} {
	return w.events
}

func (w *pollWatcher) Run(ctx context.Context) error {
	defer close(w.events)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	var lastSize int64
	var lastIdent fileIdent
	var lastModNs int64
	stat := func() (size int64, ident fileIdent, modNs int64, ok bool) {
		fi, err := os.Stat(w.path)
		if err != nil {
			return 0, fileIdent{}, 0, false
		}
		size = fi.Size()
		modNs = fi.ModTime().UnixNano()
		ident = fileIdentOf(w.path, fi)
		return size, ident, modNs, true
	}

	// Emit an initial token so readers wake up and consume any data
	// already on disk before the first poll interval elapses.
	w.send()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		size, ident, modNs, ok := stat()
		if !ok {
			// File may have been rotated away (and not yet recreated by
			// the next writer). Surface a wake-up so readers can reopen.
			if lastIdent != (fileIdent{}) || lastSize != 0 {
				lastSize = 0
				lastIdent = fileIdent{}
				lastModNs = 0
				w.send()
			}
			continue
		}
		if ident != lastIdent || size != lastSize || modNs != lastModNs {
			lastSize = size
			lastIdent = ident
			lastModNs = modNs
			w.send()
		}
	}
}

func (w *pollWatcher) send() {
	select {
	case w.events <- struct{}{}:
	default:
	}
}

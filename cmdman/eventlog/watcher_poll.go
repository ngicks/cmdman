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

	// Wake readers for content already on disk.
	w.send()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		size, ident, modNs, ok := stat()
		if !ok {
			// Rotation may briefly remove the active path.
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

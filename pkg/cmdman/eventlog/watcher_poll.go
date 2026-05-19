package eventlog

import (
	"context"
	"os"
	"time"
)

type pollWatcher struct {
	ctx      context.Context
	cancel   context.CancelFunc
	events   chan struct{}
	done     chan struct{}
	path     string
	interval time.Duration
}

func newPollWatcher(ctx context.Context, path string, interval time.Duration) *pollWatcher {
	wctx, cancel := context.WithCancel(ctx)
	w := &pollWatcher{
		ctx:      wctx,
		cancel:   cancel,
		events:   make(chan struct{}, 1),
		done:     make(chan struct{}),
		path:     path,
		interval: interval,
	}
	go w.run()
	return w
}

func (w *pollWatcher) Events() <-chan struct{} {
	return w.events
}

func (w *pollWatcher) Close() error {
	w.cancel()
	<-w.done
	return nil
}

func (w *pollWatcher) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	var lastSize int64
	var lastIno uint64
	var lastModNs int64
	stat := func() (size int64, ino uint64, modNs int64, ok bool) {
		fi, err := os.Stat(w.path)
		if err != nil {
			return 0, 0, 0, false
		}
		size = fi.Size()
		modNs = fi.ModTime().UnixNano()
		ino = inodeOf(fi)
		return size, ino, modNs, true
	}

	// Emit an initial token so readers wake up and consume any data
	// already on disk before the first poll interval elapses.
	w.send()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
		size, ino, modNs, ok := stat()
		if !ok {
			// File may have been rotated away (and not yet recreated by
			// the next writer). Surface a wake-up so readers can reopen.
			if lastIno != 0 || lastSize != 0 {
				lastSize = 0
				lastIno = 0
				lastModNs = 0
				w.send()
			}
			continue
		}
		if ino != lastIno || size != lastSize || modNs != lastModNs {
			lastSize = size
			lastIno = ino
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

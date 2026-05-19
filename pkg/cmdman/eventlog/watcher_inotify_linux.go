//go:build linux

package eventlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type inotifyWatcher struct {
	ctx    context.Context
	cancel context.CancelFunc
	events chan struct{}
	done   chan struct{}
	fd     int
	wd     int
	dir    string
	base   string
}

func newInotifyWatcher(ctx context.Context, path string) (Watcher, error) {
	if path == "" {
		return nil, fmt.Errorf("eventlog: inotify path is empty")
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("eventlog: ensure log dir for inotify: %w", err)
	}

	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("eventlog: inotify_init1: %w", err)
	}
	const mask = unix.IN_MODIFY | unix.IN_CREATE | unix.IN_MOVED_TO |
		unix.IN_DELETE | unix.IN_MOVED_FROM
	wd, err := unix.InotifyAddWatch(fd, dir, mask)
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("eventlog: inotify add watch on %s: %w", dir, err)
	}

	wctx, cancel := context.WithCancel(ctx)
	w := &inotifyWatcher{
		ctx:    wctx,
		cancel: cancel,
		events: make(chan struct{}, 1),
		done:   make(chan struct{}),
		fd:     fd,
		wd:     wd,
		dir:    dir,
		base:   base,
	}
	go w.run()
	return w, nil
}

func (w *inotifyWatcher) Events() <-chan struct{} {
	return w.events
}

func (w *inotifyWatcher) Close() error {
	w.cancel()
	// Unblocking the read loop: closing the fd is racy with concurrent
	// reads, so use a short timer in the loop (see run) and rely on ctx
	// cancellation. Here we just clean up the watch and fd.
	_, _ = unix.InotifyRmWatch(w.fd, uint32(w.wd))
	_ = unix.Close(w.fd)
	<-w.done
	return nil
}

func (w *inotifyWatcher) send() {
	select {
	case w.events <- struct{}{}:
	default:
	}
}

func (w *inotifyWatcher) run() {
	defer close(w.done)
	// Emit an initial token so readers consume existing content
	// immediately rather than waiting for the next filesystem event.
	w.send()

	buf := make([]byte, 4096)
	for {
		if err := w.ctx.Err(); err != nil {
			return
		}
		// Set a poll timeout so ctx cancellation is observed promptly even
		// when no inotify events are arriving.
		pfd := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
		_, err := unix.Poll(pfd, 250)
		if err != nil && !errors.Is(err, syscall.EINTR) {
			return
		}
		if w.ctx.Err() != nil {
			return
		}
		if pfd[0].Revents&unix.POLLIN == 0 {
			continue
		}
		n, err := unix.Read(w.fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
				continue
			}
			return
		}
		if n <= 0 {
			continue
		}
		// Scan returned events; surface a single notification per batch
		// when at least one event targets our basename.
		matched := false
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			rawEvent := (*unix.InotifyEvent)(ptrAt(buf, off))
			nameLen := int(rawEvent.Len)
			if off+unix.SizeofInotifyEvent+nameLen > n {
				break
			}
			if nameLen > 0 {
				name := bytesToString(
					buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+nameLen],
				)
				if name == w.base {
					matched = true
				}
			} else if rawEvent.Mask&unix.IN_MODIFY != 0 {
				// Watch on the dir does not produce IN_MODIFY without a
				// name, but be safe.
				matched = true
			}
			off += unix.SizeofInotifyEvent + nameLen
		}
		if matched {
			w.send()
		}
	}
}

func bytesToString(b []byte) string {
	// Strip NUL padding inotify appends to the name field.
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

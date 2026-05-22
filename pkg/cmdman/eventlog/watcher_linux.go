//go:build linux

package eventlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type inotifyWatcher struct {
	events chan struct{}
	path   string
	dir    string
	base   string
}

func newInotifyWatcher(path string) (Watcher, error) {
	if path == "" {
		return nil, fmt.Errorf("eventlog: inotify path is empty")
	}
	return &inotifyWatcher{
		events: make(chan struct{}, 1),
		path:   path,
		dir:    filepath.Dir(path),
		base:   filepath.Base(path),
	}, nil
}

func (w *inotifyWatcher) Events() <-chan struct{} {
	return w.events
}

func (w *inotifyWatcher) Run(ctx context.Context) error {
	defer close(w.events)

	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return fmt.Errorf("eventlog: ensure log dir for inotify: %w", err)
	}

	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return fmt.Errorf("eventlog: inotify_init1: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()

	const mask = unix.IN_MODIFY | unix.IN_CREATE | unix.IN_MOVED_TO |
		unix.IN_DELETE | unix.IN_MOVED_FROM
	wd, err := unix.InotifyAddWatch(fd, w.dir, mask)
	if err != nil {
		return fmt.Errorf("eventlog: inotify add watch on %s: %w", w.dir, err)
	}
	defer func() { _, _ = unix.InotifyRmWatch(fd, uint32(wd)) }()

	// Emit an initial token so readers consume existing content
	// immediately rather than waiting for the next filesystem event.
	w.send()

	buf := make([]byte, 4096)
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		// Set a poll timeout so ctx cancellation is observed promptly even
		// when no inotify events are arriving.
		pfd := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, err := unix.Poll(pfd, 250)
		if err != nil && !errors.Is(err, syscall.EINTR) {
			return fmt.Errorf("eventlog: inotify poll: %w", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		if pfd[0].Revents&unix.POLLIN == 0 {
			continue
		}
		n, err := unix.Read(fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
				continue
			}
			return fmt.Errorf("eventlog: inotify read: %w", err)
		}
		if n <= 0 {
			continue
		}
		// Scan returned events; surface a single notification per batch
		// when at least one event targets our basename.
		matched := false
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			rawEvent := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
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

func (w *inotifyWatcher) send() {
	select {
	case w.events <- struct{}{}:
	default:
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

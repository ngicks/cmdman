//go:build linux

package cmdman

import "github.com/ngicks/cmdman/pkg/cmdman/eventlog"

// defaultEventWatcherKind returns the watcher backend used when the user
// has not selected one. Linux defaults to inotify since it is available
// and avoids per-poll syscalls.
func defaultEventWatcherKind() eventlog.WatcherKind {
	return eventlog.WatcherKindInotify
}

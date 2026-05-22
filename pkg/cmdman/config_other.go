//go:build !linux

package cmdman

import "github.com/ngicks/cmdman/pkg/cmdman/eventlog"

// defaultEventWatcherKind falls back to stat-polling on platforms without
// an inotify backend wired up.
func defaultEventWatcherKind() eventlog.WatcherKind {
	return eventlog.WatcherKindPoll
}

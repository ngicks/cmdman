//go:build !linux

package eventlog

import (
	"context"
	"fmt"
)

func newInotifyWatcher(_ context.Context, _ string) (Watcher, error) {
	return nil, fmt.Errorf("eventlog: inotify watcher is only available on linux")
}

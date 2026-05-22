//go:build !linux

package eventlog

import "fmt"

func newInotifyWatcher(_ string) (Watcher, error) {
	return nil, fmt.Errorf("eventlog: inotify watcher is only available on linux")
}

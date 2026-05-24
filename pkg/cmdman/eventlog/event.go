// Package eventlog implements a process-wide append-only JSON-lines event
// log that sits next to the SQLite database. Both the service and per-
// command monitor processes append state-change events to a single file
// guarded by an advisory file lock. Subscribers watch the active file via
// inotify (Linux) or stat-polling and follow it across the writer's
// in-place rotation (one archive: events.log.1).
package eventlog

import (
	"encoding/json"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// marshalEvent renders an event as a single JSONL line (ends in '\n').
func marshalEvent(e model.Event) ([]byte, error) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	buf, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	return append(buf, '\n'), nil
}

// rotationMarker returns the rotation marker line.
func rotationMarker(now time.Time) ([]byte, error) {
	return marshalEvent(model.Event{Time: now, Type: model.EventTypeRotation})
}

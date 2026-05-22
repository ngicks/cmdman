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
)

// EventType is the discriminator for an event entry.
type EventType string

const (
	// EventTypeCreate is published when a command record is created.
	EventTypeCreate EventType = "create"
	// EventTypeRemove is published when a command record is removed.
	EventTypeRemove EventType = "remove"
	// EventTypeStart is published when a monitor begins running a command
	// iteration.
	EventTypeStart EventType = "start"
	// EventTypeRunning is published when the command subprocess has been
	// spawned and is observable as running.
	EventTypeRunning EventType = "running"
	// EventTypeExit is published when the command subprocess exits cleanly.
	EventTypeExit EventType = "exit"
	// EventTypeFail is published when the monitor or its subprocess fails.
	EventTypeFail EventType = "fail"
	// EventTypeStop is published when stop is requested for a command.
	EventTypeStop EventType = "stop"
	// EventTypeSignal is published when a raw signal is sent.
	EventTypeSignal EventType = "signal"
	// EventTypeRestart is published when the monitor reschedules a command
	// iteration under its restart policy.
	EventTypeRestart EventType = "restart"

	// eventTypeRotation is the on-disk rotation marker. It is never
	// surfaced to subscribers as an Event; readers treat it as a signal
	// to reopen the active path.
	eventTypeRotation EventType = "_rotation"
)

// IsEventType reports whether s is a known public event type.
func IsEventType(s string) bool {
	switch EventType(s) {
	case EventTypeCreate, EventTypeRemove,
		EventTypeStart, EventTypeRunning,
		EventTypeExit, EventTypeFail,
		EventTypeStop, EventTypeSignal,
		EventTypeRestart:
		return true
	}
	return false
}

// Event is a single record on the event log. JSON-encoded one per line.
type Event struct {
	Time     time.Time         `json:"time"`
	Type     EventType         `json:"type"`
	ID       string            `json:"id,omitzero"`
	Name     string            `json:"name,omitzero"`
	State    string            `json:"state,omitzero"`
	ExitCode *int              `json:"exit_code,omitzero"`
	Error    string            `json:"error,omitzero"`
	Attrs    map[string]string `json:"attrs,omitzero"`
}

// marshalEvent renders an event as a single JSONL line (ends in '\n').
func marshalEvent(e Event) ([]byte, error) {
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
	return marshalEvent(Event{Time: now, Type: eventTypeRotation})
}

package model

import "time"

// Event is a single command event.
type Event struct {
	Time     time.Time         `json:"time"`
	Type     EventType         `json:"type"`
	ID       string            `json:"id,omitzero"`
	Name     string            `json:"name,omitzero"`
	State    EventType         `json:"state,omitzero"`
	ExitCode *int              `json:"exit_code,omitzero"`
	Error    string            `json:"error,omitzero"`
	Attrs    map[string]string `json:"attrs,omitzero"`
}

// EventType is the discriminator for an event entry.
type EventType string

const (
	// EventTypeCreated is published when a command record is created.
	EventTypeCreated EventType = "created"
	// EventTypeRemoved is published when a command record is removed.
	EventTypeRemoved EventType = "removed"
	// EventTypeStarting is published when a monitor begins running a command
	// iteration. It is also the persisted state while the subprocess is being
	// started.
	EventTypeStarting EventType = "starting"
	// EventTypeStarted is published when the command subprocess has been
	// spawned and is observable as started. It is also the persisted started
	// state.
	EventTypeStarted EventType = "started"
	// EventTypeExited is published when the command subprocess exits cleanly.
	// It is also the persisted exited state.
	EventTypeExited EventType = "exited"
	// EventTypeFailed is published when the monitor or its subprocess fails.
	// It is also the persisted failed state.
	EventTypeFailed EventType = "failed"
	// EventTypeStopped is published when stop is requested for a command.
	EventTypeStopped EventType = "stopped"
	// EventTypeSignaled is published when a raw signal is sent.
	EventTypeSignaled EventType = "signaled"
	// EventTypeRotation is the on-disk event log rotation marker. Readers
	// consume it internally and do not surface it to subscribers.
	EventTypeRotation EventType = "_rotation"
)

// IsEventType reports whether s is a known public event type.
func IsEventType(s string) bool {
	switch EventType(s) {
	case EventTypeCreated, EventTypeRemoved,
		EventTypeStarting, EventTypeStarted,
		EventTypeExited, EventTypeFailed,
		EventTypeStopped, EventTypeSignaled:
		return true
	}
	return false
}

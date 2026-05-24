package model

import "time"

// Command states.
const (
	StateCreated  = "created"
	StateStarting = "starting"
	StateRunning  = "running"
	StateExited   = "exited"
	StateFailed   = "failed"
)

// Event is a single command event.
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

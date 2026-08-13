package core

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// debounceInterval coalesces a burst of lifecycle events into a single re-list.
const debounceInterval = 150 * time.Millisecond

type EventsSubscribedMsg struct {
	Stream EventStream
	Err    error
}

type EventSignalMsg struct {
	Err    error
	Closed bool
}

type ReloadTickMsg struct {
	Gen int
}

// SubscribeCmd takes its backend rather than a model so the single-widget model
// subscribes to the same lifecycle stream as the full model.
func SubscribeCmd(ctx context.Context, backend Backend) tea.Cmd {
	return func() tea.Msg {
		stream, err := backend.Events(ctx)
		return EventsSubscribedMsg{Stream: stream, Err: err}
	}
}

func WaitEventCmd(stream EventStream) tea.Cmd {
	return func() tea.Msg {
		sig, ok := <-stream.Signals()
		if !ok {
			return EventSignalMsg{Closed: true}
		}
		return EventSignalMsg{Err: sig.Err}
	}
}

func DebounceCmd(gen int) tea.Cmd {
	return tea.Tick(debounceInterval, func(time.Time) tea.Msg {
		return ReloadTickMsg{Gen: gen}
	})
}

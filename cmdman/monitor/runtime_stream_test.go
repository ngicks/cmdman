package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"gotest.tools/v3/assert"
)

// runtimeStream runs streamRuntimeState against st and captures what it sends.
type runtimeStream struct {
	sends  chan runtimeView
	states chan monitorStateChange
	// done closes when the stream returns; err is written before it closes.
	done chan struct{}
	err  error
}

// startRuntimeStream starts the stream and stops it when the test ends. Sends
// are buffered so the stream never blocks on an assertion that never reads.
func startRuntimeStream(
	t *testing.T,
	st *commandRuntimeState,
	debounce time.Duration,
) *runtimeStream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s := &runtimeStream{
		sends:  make(chan runtimeView, 16),
		states: make(chan monitorStateChange, 4),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		s.err = streamRuntimeState(ctx, st, s.states, debounce, func(v runtimeView) error {
			s.sends <- v
			return nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-s.done
	})
	return s
}

// waitEnd waits for the stream to return and yields its error.
func (s *runtimeStream) waitEnd(t *testing.T) error {
	t.Helper()
	select {
	case <-s.done:
		return s.err
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end")
		return nil
	}
}

func (s *runtimeStream) recv(t *testing.T) runtimeView {
	t.Helper()
	select {
	case v := <-s.sends:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a runtime state message")
		return runtimeView{}
	}
}

func (s *runtimeStream) assertSilent(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case v := <-s.sends:
		t.Fatalf("unexpected runtime state message %+v", v)
	case <-time.After(d):
	}
}

func TestStreamRuntimeState_SnapshotThenPush(t *testing.T) {
	st := newCommandRuntimeState()
	st.latchTitle("already set")

	stream := startRuntimeStream(t, st, defaultTitleDebounce)

	assert.Equal(t, stream.recv(t), runtimeView{Title: "already set"})

	st.setReport(reportedStatusWaiting, "needs input")
	assert.Equal(t, stream.recv(t), runtimeView{
		Title:  "already set",
		Status: reportedStatusWaiting,
		Detail: "needs input",
	})

	st.latchBell()
	assert.Equal(t, stream.recv(t), runtimeView{
		Title:      "already set",
		Status:     reportedStatusWaiting,
		Detail:     "needs input",
		BellUnread: true,
	})
}

func TestStreamRuntimeState_DebouncesTitle(t *testing.T) {
	st := newCommandRuntimeState()
	stream := startRuntimeStream(t, st, 50*time.Millisecond)
	assert.Equal(t, stream.recv(t), runtimeView{})

	// A shell retitling per prompt costs one message carrying the last title,
	// not one per prompt.
	st.latchTitle("first")
	st.latchTitle("second")
	st.latchTitle("third")

	assert.Equal(t, stream.recv(t), runtimeView{Title: "third"})
	stream.assertSilent(t, 200*time.Millisecond)
}

func TestStreamRuntimeState_SendsPendingTitleWithOtherChange(t *testing.T) {
	st := newCommandRuntimeState()
	stream := startRuntimeStream(t, st, time.Hour)
	assert.Equal(t, stream.recv(t), runtimeView{})

	st.latchTitle("building")
	stream.assertSilent(t, 100*time.Millisecond)

	// A bell is not worth waiting for, and it carries the withheld title along.
	st.latchBell()
	assert.Equal(t, stream.recv(t), runtimeView{Title: "building", BellUnread: true})
}

func TestStreamRuntimeState_SkipsInvisibleChange(t *testing.T) {
	st := newCommandRuntimeState()
	stream := startRuntimeStream(t, st, 50*time.Millisecond)
	assert.Equal(t, stream.recv(t), runtimeView{})

	// A notification bumps the generation but is not part of the view.
	st.latchNotification("agent", "done")
	stream.assertSilent(t, 200*time.Millisecond)

	st.latchTitle("visible")
	assert.Equal(t, stream.recv(t), runtimeView{Title: "visible"})
}

func TestStreamRuntimeState_FlushesPendingTitleOnExit(t *testing.T) {
	st := newCommandRuntimeState()
	stream := startRuntimeStream(t, st, time.Hour)
	assert.Equal(t, stream.recv(t), runtimeView{})

	st.latchTitle("last words")
	stream.states <- monitorStateChange{State: model.EventTypeExited}

	assert.Equal(t, stream.recv(t), runtimeView{Title: "last words"})
	assert.NilError(t, stream.waitEnd(t))
}

func TestStreamRuntimeState_EndsWhenStateChannelCloses(t *testing.T) {
	st := newCommandRuntimeState()
	stream := startRuntimeStream(t, st, defaultTitleDebounce)
	assert.Equal(t, stream.recv(t), runtimeView{})

	// The monitor closes the state broadcaster once the command is gone.
	close(stream.states)
	assert.NilError(t, stream.waitEnd(t))
}

func TestStreamRuntimeState_StaysOpenAcrossRestart(t *testing.T) {
	st := newCommandRuntimeState()
	stream := startRuntimeStream(t, st, defaultTitleDebounce)
	assert.Equal(t, stream.recv(t), runtimeView{})

	stream.states <- monitorStateChange{State: model.EventTypeRunning}
	st.reset()
	st.latchTitle("second run")

	assert.Equal(t, stream.recv(t), runtimeView{Title: "second run"})
}

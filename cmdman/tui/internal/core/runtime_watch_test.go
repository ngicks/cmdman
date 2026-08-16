package core_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"
)

func cmdInfo(id string, state model.EventType) core.CommandInfo {
	return core.CommandInfo{ID: id, Name: id, Project: "proj", State: state}
}

// recvUpdate runs the rearming receive off the test goroutine, so a watcher
// that never delivers fails the test instead of hanging it.
func recvUpdate(t *testing.T, w *core.RuntimeWatcher) core.RuntimeUpdateMsg {
	t.Helper()
	ch := make(chan tea.Msg, 1)
	go func() { ch <- core.WaitRuntimeUpdateCmd(w)() }()
	select {
	case msg := <-ch:
		u, ok := msg.(core.RuntimeUpdateMsg)
		if !ok {
			t.Fatalf("WaitRuntimeUpdateCmd delivered %T, want core.RuntimeUpdateMsg", msg)
		}
		return u
	case <-time.After(time.Second):
		t.Fatalf("no runtime update arrived")
		return core.RuntimeUpdateMsg{}
	}
}

// TestRuntimeWatcherSubscribesLiveCommands pins the subscribe half of the
// reconcile: only a command whose monitor serves a stream is dialed, and an id
// already held is not dialed again when it is listed once more.
func TestRuntimeWatcherSubscribesLiveCommands(t *testing.T) {
	fb := &coretest.FakeBackend{}
	w := core.NewRuntimeWatcher()
	t.Cleanup(func() { _ = w.Close() })

	infos := []core.CommandInfo{
		cmdInfo("a", model.EventTypeRunning),
		cmdInfo("b", model.EventTypeStarting),
		cmdInfo("c", model.EventTypeExited),
		cmdInfo("d", model.EventTypeFailed),
	}
	if dropped := w.Reconcile(t.Context(), fb, infos); dropped != nil {
		t.Errorf("first Reconcile dropped %v, want nothing", dropped)
	}
	if want := []string{"a", "b"}; !slices.Equal(fb.WatchIDs, want) {
		t.Fatalf("subscribed %v, want %v", fb.WatchIDs, want)
	}

	// A held id is not redialed, including the starting -> running transition
	// that a second list load reports.
	infos[1] = cmdInfo("b", model.EventTypeRunning)
	if dropped := w.Reconcile(t.Context(), fb, infos); dropped != nil {
		t.Errorf("second Reconcile dropped %v, want nothing", dropped)
	}
	if want := []string{"a", "b"}; !slices.Equal(fb.WatchIDs, want) {
		t.Errorf("subscribed %v after relisting the same commands, want %v", fb.WatchIDs, want)
	}
}

// TestRuntimeWatcherDropsVanishedAndStopped covers the drop half: a command
// that fell out of the list and one that left a live state both lose their
// stream, and the caller is told which ids went.
func TestRuntimeWatcherDropsVanishedAndStopped(t *testing.T) {
	fb := &coretest.FakeBackend{}
	w := core.NewRuntimeWatcher()
	t.Cleanup(func() { _ = w.Close() })

	w.Reconcile(t.Context(), fb, []core.CommandInfo{
		cmdInfo("a", model.EventTypeRunning),
		cmdInfo("b", model.EventTypeRunning),
	})
	streamA, streamB := fb.WatchStreams["a"], fb.WatchStreams["b"]
	if streamA == nil || streamB == nil {
		t.Fatalf("subscribed %v, want streams for a and b", fb.WatchIDs)
	}

	dropped := w.Reconcile(t.Context(), fb, []core.CommandInfo{
		cmdInfo("a", model.EventTypeRunning),
	})
	if want := []string{"b"}; !slices.Equal(dropped, want) {
		t.Errorf("Reconcile dropped %v after b vanished, want %v", dropped, want)
	}
	streamB.WaitClosed(t)

	dropped = w.Reconcile(t.Context(), fb, []core.CommandInfo{
		cmdInfo("a", model.EventTypeExited),
	})
	if want := []string{"a"}; !slices.Equal(dropped, want) {
		t.Errorf("Reconcile dropped %v after a exited, want %v", dropped, want)
	}
	streamA.WaitClosed(t)

	if want := []string{"a", "b"}; !slices.Equal(fb.WatchIDs, want) {
		t.Errorf("subscribed %v across the drops, want %v", fb.WatchIDs, want)
	}
}

// TestRuntimeWatcherTagsMergedUpdates pins the fan-in: every held stream funnels
// into one channel, and each update names the command it came from.
func TestRuntimeWatcherTagsMergedUpdates(t *testing.T) {
	fb := &coretest.FakeBackend{}
	w := core.NewRuntimeWatcher()
	t.Cleanup(func() { _ = w.Close() })

	w.Reconcile(t.Context(), fb, []core.CommandInfo{
		cmdInfo("a", model.EventTypeRunning),
		cmdInfo("b", model.EventTypeRunning),
	})
	fb.WatchStreams["a"].PushState(core.RuntimeStateView{Title: "alpha", Status: "working"})
	fb.WatchStreams["b"].PushState(core.RuntimeStateView{Title: "beta", BellUnread: true})

	got := map[string]core.RuntimeStateView{}
	for range 2 {
		msg := recvUpdate(t, w)
		if msg.Closed || msg.Err != nil {
			t.Fatalf("update for %q = %+v, want a pushed state", msg.ID, msg)
		}
		got[msg.ID] = msg.State
	}
	want := map[string]core.RuntimeStateView{
		"a": {Title: "alpha", Status: "working"},
		"b": {Title: "beta", BellUnread: true},
	}
	for id, wantView := range want {
		if got[id] != wantView {
			t.Errorf("update for %q = %+v, want %+v", id, got[id], wantView)
		}
	}
}

// TestRuntimeWatcherForwardsStreamError pins the error path: a terminal read
// error reaches the model tagged with its command, and the stream's end follows
// it as its own message.
func TestRuntimeWatcherForwardsStreamError(t *testing.T) {
	fb := &coretest.FakeBackend{}
	w := core.NewRuntimeWatcher()
	t.Cleanup(func() { _ = w.Close() })

	w.Reconcile(t.Context(), fb, []core.CommandInfo{cmdInfo("a", model.EventTypeRunning)})
	readErr := errors.New("watch runtime state: boom")
	fb.WatchStreams["a"].Push(core.RuntimeStateUpdate{Err: readErr})
	fb.WatchStreams["a"].EndStream()

	msg := recvUpdate(t, w)
	if msg.ID != "a" || !errors.Is(msg.Err, readErr) {
		t.Errorf("first update = %+v, want the read error tagged with a", msg)
	}
	if msg = recvUpdate(t, w); msg.ID != "a" || !msg.Closed {
		t.Errorf("second update = %+v, want a's stream reported closed", msg)
	}
}

// TestRuntimeWatcherRedialsOnlyOnReconcile covers the stream that ends on its
// own: it is dropped, nothing redials it on its own, and the next list load
// still naming the command live subscribes a fresh stream.
func TestRuntimeWatcherRedialsOnlyOnReconcile(t *testing.T) {
	fb := &coretest.FakeBackend{}
	w := core.NewRuntimeWatcher()
	t.Cleanup(func() { _ = w.Close() })

	live := []core.CommandInfo{cmdInfo("a", model.EventTypeRunning)}
	w.Reconcile(t.Context(), fb, live)
	fb.WatchStreams["a"].EndStream()

	// The Closed message is the proof the pump has dropped its entry, so the
	// redial below cannot race the drop.
	if msg := recvUpdate(t, w); msg.ID != "a" || !msg.Closed {
		t.Fatalf("update = %+v, want a's stream reported closed", msg)
	}
	if want := []string{"a"}; !slices.Equal(fb.WatchIDs, want) {
		t.Errorf("subscribed %v after the stream ended, want %v — a dead monitor "+
			"must not be redialed", fb.WatchIDs, want)
	}

	w.Reconcile(t.Context(), fb, live)
	if want := []string{"a", "a"}; !slices.Equal(fb.WatchIDs, want) {
		t.Fatalf("subscribed %v after relisting a live, want %v", fb.WatchIDs, want)
	}
	fb.WatchStreams["a"].PushState(core.RuntimeStateView{Title: "again"})
	if msg := recvUpdate(t, w); msg.ID != "a" || msg.State.Title != "again" {
		t.Errorf("update = %+v, want the redialed stream's push", msg)
	}
}

// TestRuntimeWatcherFailedSubscribeIsSilent covers criterion 5: an undialable
// monitor produces no error and no message, and the next reconcile is what
// retries it.
func TestRuntimeWatcherFailedSubscribeIsSilent(t *testing.T) {
	fb := &coretest.FakeBackend{WatchErr: errors.New("dial monitor: no such file")}
	w := core.NewRuntimeWatcher()
	t.Cleanup(func() { _ = w.Close() })

	live := []core.CommandInfo{cmdInfo("a", model.EventTypeRunning)}
	if dropped := w.Reconcile(t.Context(), fb, live); dropped != nil {
		t.Errorf("Reconcile dropped %v after a failed subscribe, want nothing", dropped)
	}
	if want := []string{"a"}; !slices.Equal(fb.WatchIDs, want) {
		t.Fatalf("subscribed %v, want %v", fb.WatchIDs, want)
	}
	if len(fb.WatchStreams) != 0 {
		t.Errorf("held %d streams after a failed subscribe, want none", len(fb.WatchStreams))
	}

	fb.WatchErr = nil
	w.Reconcile(t.Context(), fb, live)
	if want := []string{"a", "a"}; !slices.Equal(fb.WatchIDs, want) {
		t.Fatalf("subscribed %v on the retrying reconcile, want %v", fb.WatchIDs, want)
	}
	fb.WatchStreams["a"].PushState(core.RuntimeStateView{Title: "back"})
	if msg := recvUpdate(t, w); msg.ID != "a" || msg.State.Title != "back" {
		t.Errorf("update = %+v, want the retried subscribe's push", msg)
	}
}

// TestRuntimeWatcherCloseTearsDownStreams covers TUI exit: every held stream is
// released, the merged receive ends instead of parking forever, and a closed
// watcher subscribes nothing more.
func TestRuntimeWatcherCloseTearsDownStreams(t *testing.T) {
	fb := &coretest.FakeBackend{}
	w := core.NewRuntimeWatcher()

	live := []core.CommandInfo{
		cmdInfo("a", model.EventTypeRunning),
		cmdInfo("b", model.EventTypeRunning),
	}
	w.Reconcile(t.Context(), fb, live)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := fb.WatchClosed(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("closed streams %v, want both a and b", got)
	}
	if msg := recvUpdate(t, w); msg.ID != "" || !msg.Closed {
		t.Errorf("update after Close = %+v, want the watcher reported closed", msg)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if dropped := w.Reconcile(t.Context(), fb, live); dropped != nil {
		t.Errorf("Reconcile after Close dropped %v, want nothing", dropped)
	}
	if want := []string{"a", "b"}; !slices.Equal(fb.WatchIDs, want) {
		t.Errorf("subscribed %v after Close, want %v", fb.WatchIDs, want)
	}
}

// TestRuntimeWatcherCloseWithParkedPush pins the teardown against a pump that is
// mid-push: nobody is reading the merged channel, so the pump is parked on a
// send, and Close must still return rather than deadlock against it.
func TestRuntimeWatcherCloseWithParkedPush(t *testing.T) {
	fb := &coretest.FakeBackend{}
	w := core.NewRuntimeWatcher()

	w.Reconcile(t.Context(), fb, []core.CommandInfo{cmdInfo("a", model.EventTypeRunning)})
	// More pushes than the merged channel buffers, but within what the stream
	// and the merged channel hold together, so the test never blocks here while
	// the pump ends up parked mid-send.
	for range 20 {
		fb.WatchStreams["a"].PushState(core.RuntimeStateView{Title: "spam"})
	}

	done := make(chan error, 1)
	go func() { done <- w.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Close deadlocked against a parked push")
	}
	fb.WatchStreams["a"].WaitClosed(t)
}

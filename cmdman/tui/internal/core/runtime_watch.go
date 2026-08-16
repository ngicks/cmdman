package core

import (
	"context"
	"slices"
	"sync"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/cmdman/model"
)

// runtimeUpdateBuffer sizes the merged update channel. Every update carries
// what a row renders, so a pump parks on a full channel rather than dropping
// one; the buffer keeps a burst of subscribe-time snapshots from serializing
// one push per Update cycle.
const runtimeUpdateBuffer = 16

// RuntimeUpdateMsg is one runtime-state push, tagged with the command whose
// monitor pushed it. Err is a stream read error to surface without closing the
// TUI.
//
// Closed reports an ended receive: with an ID, that command's stream ended
// (the monitor left an active state) and only a later Reconcile still listing
// it live redials; with an empty ID, the watcher itself was closed and there is
// nothing left to rearm for.
type RuntimeUpdateMsg struct {
	ID     string
	State  RuntimeStateView
	Err    error
	Closed bool
}

// RuntimeWatcher holds one runtime-state stream per live command and merges
// their pushes into a single channel, so the model waits on one message source
// instead of one per command.
//
// [RuntimeWatcher.Reconcile] is the only thing that subscribes: a stream that
// ends on its own is dropped and stays dropped until a list load still names
// its command live, so a dead monitor is never redialed in a loop.
type RuntimeWatcher struct {
	// updates is written by every pump and read by WaitRuntimeUpdateCmd. Close
	// ends it once every pump has returned.
	updates chan RuntimeUpdateMsg
	pumps   errgroup.Group

	mu     sync.Mutex
	subs   map[string]*runtimeSub
	closed bool
}

// runtimeSub is one held stream plus the handle that ends its pump. The pump
// owns closing the stream, so dropping one never blocks the caller on a Close
// that talks to a monitor.
type runtimeSub struct {
	stream RuntimeStateStream
	stop   context.CancelFunc
}

func NewRuntimeWatcher() *RuntimeWatcher {
	return &RuntimeWatcher{
		updates: make(chan RuntimeUpdateMsg, runtimeUpdateBuffer),
		subs:    map[string]*runtimeSub{},
	}
}

// Reconcile brings the held streams in line with a freshly loaded list:
// commands listed in a state whose monitor serves a stream and not yet held are
// subscribed, and streams for ids that vanished from the list or left those
// states are dropped. It returns the ids this call dropped, in sorted order.
//
// The models do not evict against that return: it names only what a list load
// dropped, and a stream that ended on its own was dropped inside the watcher
// without any Reconcile ever naming it. They sweep their cache against the
// freshly loaded list instead, which covers both.
//
// A subscribe that fails is silent: the row keeps the state it last showed and
// the next Reconcile retries. ctx fathers every pump's context, so it must be
// the caller's long-lived background context — cancelling it stops the pumps
// without going through the held set, which is a teardown, not a drop.
func (w *RuntimeWatcher) Reconcile(
	ctx context.Context,
	backend Backend,
	infos []CommandInfo,
) []string {
	live := make(map[string]struct{}, len(infos))
	for _, ci := range infos {
		if hasLiveMonitor(ci.State) {
			live[ci.ID] = struct{}{}
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}

	var dropped []string
	for id, sub := range w.subs {
		if _, ok := live[id]; ok {
			continue
		}
		sub.stop()
		delete(w.subs, id)
		dropped = append(dropped, id)
	}
	slices.Sort(dropped)

	for _, ci := range infos {
		if _, ok := live[ci.ID]; !ok {
			continue
		}
		if _, held := w.subs[ci.ID]; held {
			continue
		}
		stream, err := backend.WatchRuntimeState(ctx, ci.ID)
		if err != nil {
			continue
		}
		pumpCtx, cancel := context.WithCancel(ctx)
		sub := &runtimeSub{stream: stream, stop: cancel}
		w.subs[ci.ID] = sub
		// Started under the lock, which is also where Close stops every pump:
		// that ordering is what keeps a pump from being started after Close
		// began waiting for them.
		w.pumps.Go(func() error { return w.pump(pumpCtx, ci.ID, sub) })
	}
	return dropped
}

// Close tears down every held stream and ends the merged channel, so a pending
// WaitRuntimeUpdateCmd returns instead of parking forever. Only the first call
// tears down; a later one reports success without touching anything.
func (w *RuntimeWatcher) Close() error {
	if !w.beginClose() {
		return nil
	}
	// Every pump's context is cancelled by now — by beginClose while the sub was
	// held, or by the pump itself before the send that outlives its drop — so
	// none can park on a send or a receive. Waiting for them is what makes
	// closing the merged channel safe.
	_ = w.pumps.Wait()
	close(w.updates)
	return nil
}

// beginClose stops every pump and reports whether this call is the one that
// closes the watcher. It returns with the lock released: a pump takes the lock
// to drop an ended stream, so holding it across the wait would deadlock.
func (w *RuntimeWatcher) beginClose() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	w.closed = true
	for id, sub := range w.subs {
		sub.stop()
		delete(w.subs, id)
	}
	return true
}

// pump forwards one stream's updates into the merged channel, tagged with the
// command id, until the stream ends or the sub is stopped. It owns the stream:
// however it ends, it releases both the stream and its own context.
func (w *RuntimeWatcher) pump(ctx context.Context, id string, sub *runtimeSub) error {
	defer func() {
		sub.stop()
		_ = sub.stream.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-sub.stream.Updates():
			if !ok {
				// Stopped before the drop, because dropping takes this sub out of
				// the set Close cancels: without its own cancellation the final
				// send below would be the one park nothing can end, and Close —
				// which runs on the Update goroutine — would hang the TUI on it.
				sub.stop()
				w.dropEnded(id, sub)
				w.send(ctx, RuntimeUpdateMsg{ID: id, Closed: true})
				return nil
			}
			if !w.send(ctx, RuntimeUpdateMsg{ID: id, State: u.State, Err: u.Err}) {
				return nil
			}
		}
	}
}

// send hands one update to the merged channel, reporting whether it landed. The
// lock is never held here: a parked send must not block a Reconcile or a Close.
//
// Room wins over a cancelled context, which is what the first offer is for: the
// final Closed push comes from a pump that cancelled itself to stay closable, so
// leaving both cases to one select would deliver it only about half the time
// (the two-ready pick is random). A push that finds no room still parks; only
// cancellation ends that park, dropping the push.
func (w *RuntimeWatcher) send(ctx context.Context, msg RuntimeUpdateMsg) bool {
	select {
	case w.updates <- msg:
		return true
	default:
	}
	select {
	case w.updates <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}

// dropEnded forgets a stream that ended on its own, so a later Reconcile that
// still lists the command redials it. The entry goes only when it is still this
// pump's: a Reconcile may already have dropped the id and subscribed a new one.
//
// A Reconcile landing between the stream's end and this drop sees the id as
// held and skips that redial. The window closes on the next re-list: a monitor
// leaving an active state publishes the lifecycle event that triggers one.
func (w *RuntimeWatcher) dropEnded(id string, sub *runtimeSub) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.subs[id] == sub {
		delete(w.subs, id)
	}
}

// WaitRuntimeUpdateCmd receives the next merged update. It is the single
// rearming receive over every held stream (mirroring [WaitEventCmd]): the model
// rearms it per message until one reports the watcher closed.
func WaitRuntimeUpdateCmd(w *RuntimeWatcher) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-w.updates
		if !ok {
			return RuntimeUpdateMsg{Closed: true}
		}
		return msg
	}
}

// hasLiveMonitor reports whether a listed command's state is one its monitor
// serves a runtime-state stream in. A command in any other state has no socket
// to dial, and the runtime state it last reported no longer speaks for it
// (see [LiveReport]).
func hasLiveMonitor(state model.EventType) bool {
	return state == model.EventTypeStarting || state == model.EventTypeRunning
}

package monitor

import (
	"bytes"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/ngicks/cmdman/cmdman/model"
)

// notificationEvent is a desktop notification a command asked for through
// OSC 9 or OSC 777.
type notificationEvent struct {
	Title string
	Body  string
	At    time.Time
}

// reportedStatus is the vocabulary a command uses to report its own progress
// (D12). It is set through the status RPCs, not parsed out of the output.
type reportedStatus string

const (
	reportedStatusNone    reportedStatus = ""
	reportedStatusWorking reportedStatus = "working"
	reportedStatusWaiting reportedStatus = "waiting"
	reportedStatusDone    reportedStatus = "done"
)

// runtimeSnapshot is a consistent copy of a command's latched runtime state.
// Gen changes whenever any other field does, so a consumer woken by a
// coalesced change signal can tell a real update from a spurious wake.
type runtimeSnapshot struct {
	Gen          uint64
	Title        string
	Status       reportedStatus
	Detail       string
	BellUnread   bool
	Notification *notificationEvent
}

// runtimeView is the part of a snapshot that goes on the wire. Comparing views
// instead of Gen keeps changes a consumer cannot see (a notification, a repeat)
// from costing a stream message.
type runtimeView struct {
	Title      string
	Status     reportedStatus
	Detail     string
	BellUnread bool
}

func (s runtimeSnapshot) view() runtimeView {
	return runtimeView{
		Title:      s.Title,
		Status:     s.Status,
		Detail:     s.Detail,
		BellUnread: s.BellUnread,
	}
}

// commandRuntimeState holds what a TTY command reports about itself through its
// own output: window title, unread bell, and the latest desktop notification.
// It lives only in memory and only for the current run - Monitor.runOnce resets
// it, so a restarted command never shows the previous run's title or bell.
//
// The latch methods run inside vt callbacks, which fire from screenTracker.feed
// while Monitor.logCommandOutput holds outputMu. Nothing reachable from them may
// take outputMu or re-enter the emulator: mu is a leaf lock, and the change
// signal is sent after mu is released so the callback never holds two locks.
// Readers (snapshot, subscribeChange) take mu / the broadcaster's lock only,
// never outputMu.
type commandRuntimeState struct {
	mu         sync.Mutex
	gen        uint64
	title      string
	status     reportedStatus
	detail     string
	bellUnread bool
	notif      *notificationEvent
	// attached counts live Attach streams, pairing attachBegin with attachEnd.
	// It is not per-run state: an attach survives a restart, so reset leaves it
	// alone.
	attached int

	changes *broadcaster[struct{}]
	// dispatchHook hands a captured sequence to the hook dispatcher. It is
	// called from the latch path, so it must only enqueue - see hookDispatcher.
	dispatchHook func(hookEvent)
}

func newCommandRuntimeState() *commandRuntimeState {
	return &commandRuntimeState{changes: newBroadcaster[struct{}]()}
}

// setHookSink installs the hook dispatch sink. The monitor calls it once,
// before the state observes any output, so no lock guards it.
func (s *commandRuntimeState) setHookSink(dispatch func(hookEvent)) {
	s.dispatchHook = dispatch
}

func (s *commandRuntimeState) emitHook(ev hookEvent) {
	if s.dispatchHook == nil {
		return
	}
	s.dispatchHook(ev)
}

// observe registers the emulator hooks that feed s. Title and BEL arrive as vt
// callbacks - the emulator exposes no title getter, so callbacks are the only
// route - while OSC 9 / OSC 777 have no default vt handler and are registered
// directly. Handlers return true only to keep the emulator from logging them as
// unhandled; passthrough to attached viewers happens on the byte path, which
// the emulator does not gate.
func (s *commandRuntimeState) observe(term *vt.Emulator) {
	term.SetCallbacks(vt.Callbacks{
		Bell:  s.latchBell,
		Title: s.latchTitle,
	})
	term.RegisterOscHandler(9, func(data []byte) bool {
		if body, ok := parseOsc9Notification(data); ok {
			s.latchNotification("", body)
		}
		return true
	})
	term.RegisterOscHandler(777, func(data []byte) bool {
		if title, body, ok := parseOsc777Notification(data); ok {
			s.latchNotification(title, body)
		}
		return true
	})
}

// snapshot returns the current state. The returned notification is never
// mutated in place - a new one replaces the pointer.
func (s *commandRuntimeState) snapshot() runtimeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return runtimeSnapshot{
		Gen:          s.gen,
		Title:        s.title,
		Status:       s.status,
		Detail:       s.detail,
		BellUnread:   s.bellUnread,
		Notification: s.notif,
	}
}

// subscribeChange returns a wake channel and its unsubscribe function. The
// channel carries wakes, not values: a woken consumer reads snapshot for the
// current state, so a dropped wake on a full channel loses nothing.
func (s *commandRuntimeState) subscribeChange() (<-chan struct{}, func()) {
	return s.changes.Subscribe()
}

// latchTitle records the window title. The hook fires on a real change only: a
// shell retitles on every prompt, and a title is a value, not an occurrence.
func (s *commandRuntimeState) latchTitle(title string) {
	changed := s.mutate(func() bool {
		if s.title == title {
			return false
		}
		s.title = title
		return true
	})
	if changed {
		s.emitHook(hookEvent{Kind: model.HookEventTitle, Title: title})
	}
}

// latchBell marks the bell unread. It latches even while a viewer is attached:
// D11 reads "read" as "I actually looked at that command", and a stream that
// has been open for hours is not a look - a mux dashboard pane runs
// `cmdman attach` for its whole lifetime, and the monitor cannot see which pane
// is on screen. Opening the attach is the look, which is why attachBegin
// clears; a bell ringing after it is news again. Suppressing the latch for the
// length of the stream instead made the badge dead for every dashboarded
// command. Selection-level clearing (D22) is the switcher's, not the monitor's.
// The hook fires per bell either way: a repeat while already unread changes no
// state but is still a bell.
func (s *commandRuntimeState) latchBell() {
	s.mutate(func() bool {
		if s.bellUnread {
			return false
		}
		s.bellUnread = true
		return true
	})
	s.emitHook(hookEvent{Kind: model.HookEventBell})
}

// latchNotification records the newest notification. Every notification is an
// event of its own, so a repeat of an identical one still counts as a change.
func (s *commandRuntimeState) latchNotification(title, body string) {
	now := time.Now()
	s.mutate(func() bool {
		s.notif = &notificationEvent{Title: title, Body: body, At: now}
		return true
	})
	s.emitHook(hookEvent{Kind: model.HookEventNotification, Title: title, Body: body})
}

// clearBellLocked marks the bell read. Opening an attach is the only thing that
// reads a bell inside the monitor (D11), so there is no standalone dismiss;
// clearing on project selection (D22) belongs to the switcher, which is the only
// side that knows what is actually on screen.
func (s *commandRuntimeState) clearBellLocked() bool {
	if !s.bellUnread {
		return false
	}
	s.bellUnread = false
	return true
}

// attachBegin records a viewer looking at the command and clears the bell:
// opening an attach is what reads its bell (D11). It reads once, at the open -
// see latchBell for why the stream staying open afterwards does not keep
// reading. Every caller must pair it with attachEnd.
func (s *commandRuntimeState) attachBegin() {
	s.mutate(func() bool {
		s.attached++
		return s.clearBellLocked()
	})
}

func (s *commandRuntimeState) attachEnd() {
	s.mutate(func() bool {
		if s.attached > 0 {
			s.attached--
		}
		return false
	})
}

// setReport records the status the command reports about itself, replacing both
// fields at once so a report never mixes a new status with a stale detail.
func (s *commandRuntimeState) setReport(status reportedStatus, detail string) {
	s.mutate(func() bool {
		if s.status == status && s.detail == detail {
			return false
		}
		s.status, s.detail = status, detail
		return true
	})
}

// clearReport drops the reported status.
func (s *commandRuntimeState) clearReport() {
	s.setReport(reportedStatusNone, "")
}

// reset drops the state so the next run starts clean. The reported status goes
// with it (D13): a stale "working" on a process that has since restarted is
// worse than no status at all.
func (s *commandRuntimeState) reset() {
	s.mutate(func() bool {
		if s.title == "" && s.status == reportedStatusNone && s.detail == "" &&
			!s.bellUnread && s.notif == nil {
			return false
		}
		s.title, s.status, s.detail, s.bellUnread, s.notif = "", reportedStatusNone, "", false, nil
		return true
	})
}

// mutate applies fn under mu, bumping the generation and waking subscribers
// when fn reports an actual change. The wake is sent after mu is released.
// It returns what fn reported.
func (s *commandRuntimeState) mutate(fn func() bool) bool {
	s.mu.Lock()
	changed := fn()
	if changed {
		s.gen++
	}
	s.mu.Unlock()
	if changed {
		s.changes.Send(struct{}{})
	}
	return changed
}

// parseOsc9Notification reads the iTerm2 notification form `9;<message>`. The
// whole remainder is the message - messages contain semicolons - and ConEmu's
// progress form (`9;4;…`) is skipped: it is not a notification.
func parseOsc9Notification(data []byte) (body string, ok bool) {
	rest, found := bytes.CutPrefix(data, []byte("9;"))
	if !found || len(rest) == 0 {
		return "", false
	}
	if rest[0] == '4' && (len(rest) == 1 || rest[1] == ';') {
		return "", false
	}
	return string(rest), true
}

// parseOsc777Notification reads the urxvt/kitty form `777;notify;<title>;<body>`;
// other 777 subtypes are ignored. The body is the unsplit remainder so its
// semicolons survive.
func parseOsc777Notification(data []byte) (title, body string, ok bool) {
	parts := bytes.SplitN(data, []byte{';'}, 4)
	if len(parts) < 3 || !bytes.Equal(parts[1], []byte("notify")) {
		return "", "", false
	}
	if len(parts) == 4 {
		body = string(parts[3])
	}
	return string(parts[2]), body, true
}

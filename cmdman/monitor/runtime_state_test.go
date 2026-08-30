package monitor

import (
	"testing"
	"unicode/utf8"

	pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
	"google.golang.org/protobuf/proto"
	"gotest.tools/v3/assert"
)

// runtimeStateFeed wires a state to a real emulator, as the monitor does, and
// returns a writer of raw command output.
func runtimeStateFeed(t *testing.T) (*commandRuntimeState, func(string)) {
	t.Helper()
	st := newCommandRuntimeState()
	tr := newScreenTracker(80, 24, st)
	t.Cleanup(tr.close)
	return st, func(raw string) { tr.feed([]byte(raw)) }
}

// woke consumes one pending change signal. Latching signals synchronously, so
// an empty channel proves no change happened.
func woke(changed <-chan struct{}) bool {
	select {
	case <-changed:
		return true
	default:
		return false
	}
}

func TestCommandRuntimeState_LatchesTitle(t *testing.T) {
	st, feed := runtimeStateFeed(t)

	feed("\x1b]2;first title\x07")
	assert.Equal(t, st.snapshot().Title, "first title")
	// The BEL that terminated the OSC above is a string terminator, not a bell.
	assert.Equal(t, st.snapshot().BellUnread, false)

	feed("\x1b]0;second title\x1b\\")
	assert.Equal(t, st.snapshot().Title, "second title")
}

func TestCommandRuntimeState_LatchesCwd(t *testing.T) {
	st, feed := runtimeStateFeed(t)

	// Nothing reported yet is distinguishable from an explicitly empty payload.
	assert.Equal(t, st.snapshot().CwdSet, false)

	feed("\x1b]7;file://host/tmp/first\x07")
	snap := st.snapshot()
	// The payload is stored verbatim: a replay to a viewer re-emits these bytes.
	assert.Equal(t, snap.Cwd, "file://host/tmp/first")
	assert.Equal(t, snap.CwdSet, true)

	feed("\x1b]7;file://host/tmp/second\x1b\\")
	assert.Equal(t, st.snapshot().Cwd, "file://host/tmp/second")

	// A shell re-emits OSC 7 on every prompt; the same directory is not news.
	changed, unsub := st.subscribeChange()
	t.Cleanup(unsub)
	feed("\x1b]7;file://host/tmp/second\x07")
	assert.Assert(t, !woke(changed))

	// The latch sanitizes whatever the emulator hands it, as the title does.
	st.latchCwd("file://host/tmp/\xe2")
	assert.Equal(t, st.snapshot().Cwd, "file://host/tmp/�")
}

func TestCommandRuntimeState_SeedsCwdFromConfiguredDir(t *testing.T) {
	st, feed := runtimeStateFeed(t)

	st.seedCwd("/home/me/my project")
	snap := st.snapshot()
	assert.Equal(t, snap.Cwd, "file://localhost/home/me/my%20project")
	assert.Equal(t, snap.CwdSet, true)
	// What goes on the wire is the directory that was configured, back in the
	// form a reader can use.
	assert.Equal(t, snap.view().Cwd, "/home/me/my project")

	// The seed is only a baseline: what the command reports itself wins.
	feed("\x1b]7;file://host/tmp/real\x07")
	assert.Equal(t, st.snapshot().Cwd, "file://host/tmp/real")

	// An empty directory is not a directory; seeding it would report the bare
	// `file://localhost` the empty path encodes to.
	st.reset()
	st.seedCwd("")
	assert.Equal(t, st.snapshot().CwdSet, false)
}

func TestCwdPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "host is dropped",
			payload: "file://somehost/tmp/x",
			want:    "/tmp/x",
		},
		{
			name:    "empty host",
			payload: "file:///tmp/x",
			want:    "/tmp/x",
		},
		{
			name:    "percent escapes are decoded",
			payload: "file://localhost/home/me/my%20project",
			want:    "/home/me/my project",
		},
		{
			name:    "no path",
			payload: "file://localhost",
			want:    "",
		},
		{
			// A payload that never reached the latch reads as unknown, not as
			// the root directory.
			name:    "nothing reported",
			payload: "",
			want:    "",
		},
		{
			// Some shells emit a bare path. Guessing that it is a local
			// directory would make the field mean two different things.
			name:    "bare path is not a file url",
			payload: "/tmp/x",
			want:    "",
		},
		{
			name:    "other scheme",
			payload: "http://example.com/tmp/x",
			want:    "",
		},
		{
			name:    "unparseable payload",
			payload: "file://host/%zz",
			want:    "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, cwdPath(tc.payload), tc.want)
		})
	}
}

// A payload the view cannot parse costs the wire field, not the replay: an
// attached viewer still gets the bytes the command sent.
func TestCommandRuntimeState_UnparseableCwdIsEmptyOnTheWire(t *testing.T) {
	st, feed := runtimeStateFeed(t)

	feed("\x1b]7;file://host/%zz\x07")
	snap := st.snapshot()
	assert.Equal(t, snap.Cwd, "file://host/%zz")
	assert.Equal(t, snap.view().Cwd, "")
}

func TestCommandRuntimeState_SanitizesInvalidUTF8(t *testing.T) {
	st, feed := runtimeStateFeed(t)

	// The vendored parser keeps a 0x9C continuation byte (the middle of "✳",
	// E2 9C B3) inside the OSC string, so a glyph title arrives whole. The
	// unpatched parser cut it there and handed the latch the invalid fragment
	// "\xe2" — which the sanitizing below still guards against.
	feed("\x1b]0;✳ Mermaid-cli lint hook\x07")
	title := st.snapshot().Title
	assert.Assert(t, utf8.ValidString(title), "latched title %q is invalid UTF-8", title)
	assert.Equal(t, title, "✳ Mermaid-cli lint hook")

	// The latch itself must sanitize whatever an emulator hands it: a raw
	// invalid fragment stores as a valid-UTF-8, visibly degraded title.
	st.latchTitle("\xe2")
	assert.Equal(t, st.snapshot().Title, "�")

	// Repeating the mangled title is still not a change.
	changed, unsub := st.subscribeChange()
	t.Cleanup(unsub)
	st.latchTitle("\xe2")
	assert.Assert(t, !woke(changed))

	// A notification's title and body come off the same raw byte path.
	st.latchNotification("\xe2 alert", "body \xff")
	notif := st.snapshot().Notification
	assert.Equal(t, notif.Title, "� alert")
	assert.Equal(t, notif.Body, "body �")
}

// TestProtoRuntimeState_MarshalsMangledTitle pins the consequence the
// sanitizing exists for: one invalid-UTF-8 latched string used to fail
// proto.Marshal of every Status / WatchRuntimeState response, blanking all
// runtime columns for the command instead of just degrading the title. The
// mangled fragment is latched directly — the vendored parser no longer
// produces one from a glyph title, and the guard must hold either way.
func TestProtoRuntimeState_MarshalsMangledTitle(t *testing.T) {
	st, feed := runtimeStateFeed(t)

	feed("\x1b]0;✳ done\x07")
	st.latchTitle("\xe2")

	_, err := proto.Marshal(&pb.StatusResponse{
		RuntimeState: protoRuntimeState(st.snapshot().view()),
	})
	assert.NilError(t, err)
}

func TestCommandRuntimeState_LatchesBell(t *testing.T) {
	st, feed := runtimeStateFeed(t)

	feed("some output\a")
	snap := st.snapshot()
	assert.Equal(t, snap.BellUnread, true)

	// A second bell while unread is not a state change.
	feed("\a")
	assert.Equal(t, st.snapshot().Gen, snap.Gen)

	st.attachBegin()
	assert.Equal(t, st.snapshot().BellUnread, false)
}

func TestCommandRuntimeState_LatchesNotifications(t *testing.T) {
	for _, tc := range []struct {
		name      string
		raw       string
		wantTitle string
		wantBody  string
		wantLatch bool
	}{
		{
			name:      "osc 9 message",
			raw:       "\x1b]9;build finished\x07",
			wantBody:  "build finished",
			wantLatch: true,
		},
		{
			// Splitting on ';' would truncate the message.
			name:      "osc 9 message with semicolons",
			raw:       "\x1b]9;done; deploy?\x1b\\",
			wantBody:  "done; deploy?",
			wantLatch: true,
		},
		{
			name: "osc 9 progress is not a notification",
			raw:  "\x1b]9;4;1;50\x07",
		},
		{
			name:      "osc 777 notify",
			raw:       "\x1b]777;notify;Agent;needs input; now\x07",
			wantTitle: "Agent",
			wantBody:  "needs input; now",
			wantLatch: true,
		},
		{
			name:      "osc 777 notify without body",
			raw:       "\x1b]777;notify;Agent\x07",
			wantTitle: "Agent",
			wantLatch: true,
		},
		{
			name: "osc 777 other subtype",
			raw:  "\x1b]777;precmd\x07",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, feed := runtimeStateFeed(t)

			feed(tc.raw)

			got := st.snapshot().Notification
			if !tc.wantLatch {
				assert.Assert(t, got == nil, "latched %+v", got)
				return
			}
			assert.Assert(t, got != nil)
			assert.Equal(t, got.Title, tc.wantTitle)
			assert.Equal(t, got.Body, tc.wantBody)
			assert.Assert(t, !got.At.IsZero())
		})
	}
}

func TestCommandRuntimeState_SignalsChange(t *testing.T) {
	st, feed := runtimeStateFeed(t)
	changed, unsub := st.subscribeChange()
	t.Cleanup(unsub)

	assert.Assert(t, !woke(changed))

	feed("\x1b]2;title\x07")
	assert.Assert(t, woke(changed))
	gen := st.snapshot().Gen
	assert.Assert(t, gen > 0)

	// Re-titling to the same string changes nothing worth waking for.
	feed("\x1b]2;title\x07")
	assert.Assert(t, !woke(changed))
	assert.Equal(t, st.snapshot().Gen, gen)

	// Every notification is its own event, even a repeat.
	feed("\x1b]9;ping\x07")
	assert.Assert(t, woke(changed))
	feed("\x1b]9;ping\x07")
	assert.Assert(t, woke(changed))
}

func TestCommandRuntimeState_ReportedStatus(t *testing.T) {
	st, _ := runtimeStateFeed(t)
	changed, unsub := st.subscribeChange()
	t.Cleanup(unsub)

	st.setReport(reportedStatusWorking, "step 1/3")
	snap := st.snapshot()
	assert.Equal(t, snap.Status, reportedStatusWorking)
	assert.Equal(t, snap.Detail, "step 1/3")
	assert.Assert(t, woke(changed))

	// Re-reporting the same thing is not a change.
	st.setReport(reportedStatusWorking, "step 1/3")
	assert.Equal(t, st.snapshot().Gen, snap.Gen)
	assert.Assert(t, !woke(changed))

	// A report replaces both fields: a new status never keeps the old detail.
	st.setReport(reportedStatusWaiting, "")
	assert.Equal(t, st.snapshot().Detail, "")

	st.clearReport()
	assert.Equal(t, st.snapshot().Status, reportedStatusNone)
	assert.Assert(t, woke(changed))
}

func TestCommandRuntimeState_AttachReadsBell(t *testing.T) {
	st, feed := runtimeStateFeed(t)
	changed, unsub := st.subscribeChange()
	t.Cleanup(unsub)

	feed("\a")
	assert.Equal(t, st.snapshot().BellUnread, true)
	for woke(changed) {
	}

	st.attachBegin()
	assert.Equal(t, st.snapshot().BellUnread, false)
	assert.Assert(t, woke(changed))

	// A bell that rings during a still-open attach latches: opening the stream
	// was the look (D11), the stream sitting open afterwards is not - a mux
	// dashboard pane holds one open for the whole life of the pane.
	feed("\a")
	assert.Equal(t, st.snapshot().BellUnread, true)
	assert.Assert(t, woke(changed))

	// Opening another one reads it again, and the one after that latches too.
	st.attachBegin()
	assert.Equal(t, st.snapshot().BellUnread, false)
	feed("\a")
	assert.Equal(t, st.snapshot().BellUnread, true)

	// Ending an attach reads nothing: only opening one does.
	st.attachEnd()
	st.attachEnd()
	assert.Equal(t, st.snapshot().BellUnread, true)
}

func TestCommandRuntimeState_ResetDropsPreviousRun(t *testing.T) {
	st, feed := runtimeStateFeed(t)
	changed, unsub := st.subscribeChange()
	t.Cleanup(unsub)

	feed("\x1b]2;title\x07\a\x1b]9;ping\x07\x1b]7;file://host/tmp/old\x07")
	st.setReport(reportedStatusWorking, "still going")
	for woke(changed) {
	}

	st.reset()

	snap := st.snapshot()
	assert.Equal(t, snap.Title, "")
	assert.Equal(t, snap.Cwd, "")
	assert.Equal(t, snap.CwdSet, false)
	assert.Equal(t, snap.BellUnread, false)
	assert.Assert(t, snap.Notification == nil)
	// D13: the reported status dies with the run it described.
	assert.Equal(t, snap.Status, reportedStatusNone)
	assert.Equal(t, snap.Detail, "")
	assert.Assert(t, woke(changed))

	// A clean state stays clean, without waking anyone.
	st.reset()
	assert.Equal(t, st.snapshot().Gen, snap.Gen)
	assert.Assert(t, !woke(changed))
}

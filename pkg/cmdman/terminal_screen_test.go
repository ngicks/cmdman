package cmdman

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// renderVia replays raw bytes into a fresh emulator (as a re-attaching client
// would) and returns its rendered screen.
func renderVia(cols, rows int, raw []byte) string {
	term := vt.NewEmulator(cols, rows)
	_, _ = term.Write(raw)
	return term.Render()
}

// TestScreenTrackerSnapshotReconstructsScrolledOutChrome is the regression for
// the "preview breaks when transitioning among commands" bug: static chrome that
// a program painted once and then pushed out of the byte scrollback must still
// appear in the snapshot handed to a re-attaching client, because the tracker
// keeps live screen state instead of replaying a rotated byte window.
func TestScreenTrackerSnapshotReconstructsScrolledOutChrome(t *testing.T) {
	const cols, rows = 80, 24
	tr := newScreenTracker(cols, rows)
	t.Cleanup(tr.close)

	// Paint static chrome once (row 1 + row 3), then emit many incremental
	// single-region updates (row 10) as a full-screen program would.
	tr.feed([]byte("\x1b[?1049h\x1b[2J\x1b[H"))
	tr.feed([]byte("\x1b[1;1HHEADER-ONCE"))
	tr.feed([]byte("\x1b[3;1H+--frame--+"))
	var raw strings.Builder
	raw.WriteString("\x1b[?1049h\x1b[2J\x1b[H\x1b[1;1HHEADER-ONCE\x1b[3;1H+--frame--+")
	for i := range 300 {
		seq := "\x1b[10;1HUPDATE-" + strconv.Itoa(
			i,
		) + "-paddddddddddddddddddddddddddddddddddddddding"
		tr.feed([]byte(seq))
		raw.WriteString(seq)
	}
	tr.feed([]byte("\x1b[10;1HFINAL-LINE"))
	raw.WriteString("\x1b[10;1HFINAL-LINE")

	snap := tr.snapshot()
	if snap == nil {
		t.Fatal("snapshot returned nil for a healthy tracker")
	}

	got := renderVia(cols, rows, snap)
	for _, want := range []string{"HEADER-ONCE", "+--frame--+", "FINAL-LINE"} {
		if !strings.Contains(got, want) {
			t.Fatalf("snapshot render missing %q; got:\n%s", want, got)
		}
	}

	// Sanity: a re-attaching client that only received the LAST 256 raw bytes
	// (a rotated ring) reconstructs a broken frame missing the one-time chrome —
	// which is exactly the bug the snapshot fixes.
	full := raw.String()
	tail := full[len(full)-256:]
	broken := renderVia(cols, rows, []byte(tail))
	if strings.Contains(broken, "HEADER-ONCE") {
		t.Skip("ring window unexpectedly retained the header; adjust the fixture")
	}
}

// TestScreenTrackerSnapshotMatchesFullReplay proves the snapshot is a faithful
// stand-in for replaying the entire raw history: both reconstruct the same
// screen on a fresh client emulator.
func TestScreenTrackerSnapshotMatchesFullReplay(t *testing.T) {
	const cols, rows = 40, 10
	tr := newScreenTracker(cols, rows)
	t.Cleanup(tr.close)

	var raw strings.Builder
	write := func(s string) {
		tr.feed([]byte(s))
		raw.WriteString(s)
	}
	write("\x1b[2J\x1b[H")
	write("\x1b[1;1H\x1b[31mred title\x1b[m")
	write("\x1b[5;3Hmiddle row")
	write("\x1b[10;1Hbottom")

	fromSnapshot := renderVia(cols, rows, tr.snapshot())
	fromFullReplay := renderVia(cols, rows, []byte(raw.String()))
	if fromSnapshot != fromFullReplay {
		t.Fatalf("snapshot render differs from full replay:\nsnapshot:\n%s\nreplay:\n%s",
			fromSnapshot, fromFullReplay)
	}
}

// TestScreenTrackerFeedDoesNotDeadlockOnQuery guards the D12 hazard: a program
// that emits a terminal query (here DA1, ESC[c) makes the emulator write a reply
// into its unbuffered response pipe; without the drain goroutine that write would
// block feed() forever under the monitor's outputMu. feed must return promptly.
func TestScreenTrackerFeedDoesNotDeadlockOnQuery(t *testing.T) {
	tr := newScreenTracker(80, 24)
	t.Cleanup(tr.close)

	done := make(chan struct{})
	go func() {
		for range 50 {
			tr.feed([]byte("\x1b[c"))    // DA1 device-attributes query
			tr.feed([]byte("\x1b[6n"))   // cursor-position report query
			tr.feed([]byte("hello\r\n")) // ordinary output
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("feed deadlocked draining the emulator's query responses")
	}
	if !tr.healthy {
		t.Fatal("tracker went unhealthy feeding ordinary queries")
	}
}

// TestScreenTrackerNilSafe verifies the nil-receiver guards so the monitor can
// call methods before a run has started a mirror (non-TTY commands keep it nil).
func TestScreenTrackerNilSafe(t *testing.T) {
	var tr *screenTracker
	tr.feed([]byte("x"))
	tr.resize(10, 10)
	tr.close()
	if snap := tr.snapshot(); snap != nil {
		t.Fatalf("nil tracker snapshot must be nil, got %q", snap)
	}
}

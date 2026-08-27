package monitor

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	ansi "github.com/ngicks/cmdman/internal/third_party/charmbracelet-x-ansi"
	vt "github.com/ngicks/cmdman/internal/third_party/charmbracelet-x-vt"
)

// renderVia replays raw in a fresh cols-by-rows emulator and returns its screen.
func renderVia(cols, rows int, raw []byte) string {
	term := vt.NewEmulator(cols, rows)
	_, _ = term.Write(raw)
	return term.Render()
}

func TestScreenTrackerSnapshotReconstructsScrolledOutChrome(t *testing.T) {
	const cols, rows = 80, 24
	tr := newScreenTracker(cols, rows, newCommandRuntimeState())
	t.Cleanup(tr.close)

	// Paint chrome once, then push it out of the raw byte window.
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

	// A rotated raw window cannot reconstruct the one-time chrome.
	full := raw.String()
	tail := full[len(full)-256:]
	broken := renderVia(cols, rows, []byte(tail))
	if strings.Contains(broken, "HEADER-ONCE") {
		t.Skip("ring window unexpectedly retained the header; adjust the fixture")
	}
}

func TestScreenTrackerSnapshotMatchesFullReplay(t *testing.T) {
	const cols, rows = 40, 10
	tr := newScreenTracker(cols, rows, newCommandRuntimeState())
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

func TestScreenTrackerFeedDoesNotDeadlockOnQuery(t *testing.T) {
	tr := newScreenTracker(80, 24, newCommandRuntimeState())
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

func TestScreenTrackerCapture(t *testing.T) {
	const cols, rows = 20, 5

	// Ten lines on a five-row screen leave line-0..line-5 in history.
	scrolled := func(tr *screenTracker) {
		for i := range 10 {
			tr.feed(fmt.Appendf(nil, "line-%d\r\n", i))
		}
	}
	pad := func(s string) string { return s + strings.Repeat(" ", cols-len(s)) }

	const (
		visible = "line-6\nline-7\nline-8\nline-9\n\n"
		history = "line-0\nline-1\nline-2\nline-3\nline-4\nline-5\n"
	)

	for _, tc := range []struct {
		name    string
		feed    func(*screenTracker)
		opts    captureOptions
		want    string
		wantErr error
	}{
		{
			name: "visible screen by default",
			feed: scrolled,
			want: visible,
		},
		{
			name: "negative start reaches into history",
			feed: scrolled,
			opts: captureOptions{start: -2, startSet: true},
			want: "line-4\nline-5\n" + visible,
		},
		{
			name: "start dash takes the whole history",
			feed: scrolled,
			opts: captureOptions{startWholeHistory: true},
			want: history + visible,
		},
		{
			name: "out of range start and end clamp",
			feed: scrolled,
			opts: captureOptions{start: -99, startSet: true, end: 99, endSet: true},
			want: history + visible,
		},
		{
			name: "end dash takes the bottom of the visible screen",
			feed: scrolled,
			opts: captureOptions{end: -4, endSet: true, endWholeScreen: true},
			want: visible,
		},
		{
			name: "end before start captures nothing",
			feed: scrolled,
			opts: captureOptions{start: 3, startSet: true, end: 1, endSet: true},
			want: "",
		},
		{
			name: "trailing spaces trimmed across history and screen",
			feed: scrolled,
			opts: captureOptions{start: -1, startSet: true, end: 0, endSet: true},
			want: "line-5\nline-6\n",
		},
		{
			name: "trailing spaces preserved across history and screen",
			feed: scrolled,
			opts: captureOptions{
				start: -1, startSet: true,
				end: 0, endSet: true,
				preserveTrailingSpaces: true,
			},
			want: pad("line-5") + "\n" + pad("line-6") + "\n",
		},
		{
			name: "wide runes keep their columns",
			feed: func(tr *screenTracker) { tr.feed([]byte("あいu")) },
			opts: captureOptions{end: 0, endSet: true},
			want: "あいu\n",
		},
		{
			name: "wide runes pad to the display width",
			feed: func(tr *screenTracker) { tr.feed([]byte("あいu")) },
			opts: captureOptions{end: 0, endSet: true, preserveTrailingSpaces: true},
			want: "あいu" + strings.Repeat(" ", cols-5) + "\n",
		},
		{
			name:    "alt screen requested while inactive",
			feed:    scrolled,
			opts:    captureOptions{altScreen: true},
			wantErr: errNoAltScreen,
		},
		{
			name: "alt screen requested while inactive stays quiet",
			feed: scrolled,
			opts: captureOptions{altScreen: true, quiet: true},
			want: "",
		},
		{
			name: "active alt screen hides history",
			feed: func(tr *screenTracker) {
				scrolled(tr)
				tr.feed([]byte("\x1b[?1049h\x1b[2J\x1b[HALT-0\r\nALT-1"))
			},
			opts: captureOptions{altScreen: true, startWholeHistory: true},
			want: "ALT-0\nALT-1\n\n\n\n",
		},
		{
			name:    "unhealthy tracker",
			feed:    func(tr *screenTracker) { tr.healthy = false },
			wantErr: errScreenUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newScreenTracker(cols, rows, newCommandRuntimeState())
			t.Cleanup(tr.close)
			tc.feed(tr)

			got, err := tr.capture(tc.opts)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("capture error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if string(got) != tc.want {
				t.Fatalf("capture =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestScreenTrackerCaptureEscapes(t *testing.T) {
	const cols, rows = 20, 2
	tr := newScreenTracker(cols, rows, newCommandRuntimeState())
	t.Cleanup(tr.close)
	tr.feed([]byte("\x1b[31mRED\x1b[m ok"))

	opts := captureOptions{escapes: true, end: 0, endSet: true}
	styled, err := tr.capture(opts)
	if err != nil {
		t.Fatalf("styled capture: %v", err)
	}
	got := string(styled)
	if !strings.HasPrefix(got, ansi.ResetStyle) {
		t.Fatalf("styled line must open with a reset, got %q", got)
	}
	if !strings.HasSuffix(got, ansi.ResetStyle+"\n") {
		t.Fatalf("styled line must close with a reset, got %q", got)
	}
	if !strings.Contains(got, "31m") {
		t.Fatalf("styled capture lost the red attribute, got %q", got)
	}

	opts.escapes = false
	plain, err := tr.capture(opts)
	if err != nil {
		t.Fatalf("plain capture: %v", err)
	}
	if string(plain) != "RED ok\n" {
		t.Fatalf("plain capture = %q, want %q", plain, "RED ok\n")
	}
	if stripped := ansi.Strip(got); stripped != string(plain) {
		t.Fatalf("stripped styled capture = %q, want %q", stripped, plain)
	}

	// A blank row carries no attributes worth restating.
	blank, err := tr.capture(captureOptions{escapes: true, start: 1, startSet: true})
	if err != nil {
		t.Fatalf("blank capture: %v", err)
	}
	if string(blank) != "\n" {
		t.Fatalf("blank styled row = %q, want %q", blank, "\n")
	}
}

func TestScreenTrackerNilSafe(t *testing.T) {
	var tr *screenTracker
	tr.feed([]byte("x"))
	tr.resize(10, 10)
	tr.close()
	if snap := tr.snapshot(); snap != nil {
		t.Fatalf("nil tracker snapshot must be nil, got %q", snap)
	}
	if out, err := tr.capture(captureOptions{}); out != nil ||
		!errors.Is(err, errScreenUnavailable) {
		t.Fatalf("nil tracker capture = (%q, %v), want (nil, %v)", out, err, errScreenUnavailable)
	}
}

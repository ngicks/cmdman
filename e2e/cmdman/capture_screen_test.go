package cmdman_test

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ansiSequence matches the CSI sequences a capture with -e emits: the style
// diffs of the rendered cells and the reset that brackets each nonempty row.
var ansiSequence = regexp.MustCompile("\x1b\\[[0-9;:]*[A-Za-z]")

func stripANSI(s string) string {
	return ansiSequence.ReplaceAllString(s, "")
}

// captureScreenScript paints three things a byte-stream reader could not
// reproduce: a plain marker, a colored one, and text addressed to row 10
// column 5, which only a terminal emulator places there.
const captureScreenScript = `printf 'CAPTURE_MARKER\n'
printf '\033[31mRED_MARKER\033[m\n'
printf '\033[10;5HCOL5_MARKER'
sleep 300`

// TestCaptureScreen_RendersRunningScreen captures the screen of a running TTY
// command, as plain text and with -e.
func TestCaptureScreen_RendersRunningScreen(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-t", "-n", "capture-render", "--",
		"/bin/sh", "-c", captureScreenScript)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	// The screen is rendered from what the child has written so far, so poll
	// until the last thing it paints has arrived.
	var plain string
	waitUntil(t, defaultTimeout, func() bool {
		out, _, err := env.exec(ctx, "capture-screen", id)
		if err != nil || !strings.Contains(out, "COL5_MARKER") {
			return false
		}
		plain = out
		return true
	}, "capture-screen never rendered the child's screen")

	t.Run("PlainText", func(t *testing.T) {
		if !strings.Contains(plain, "CAPTURE_MARKER") {
			t.Fatalf("capture missing the plain marker:\n%s", plain)
		}
		if !strings.Contains(plain, "RED_MARKER") {
			t.Fatalf("capture missing the colored marker's text:\n%s", plain)
		}
		// Column 5 means four leading spaces, which the default capture keeps.
		if !strings.Contains(plain, "\n    COL5_MARKER") {
			t.Fatalf("cursor-addressed text not placed at column 5:\n%q", plain)
		}
		if strings.ContainsRune(plain, 0x1b) {
			t.Fatalf("default capture leaked an escape sequence:\n%q", plain)
		}
	})

	t.Run("Escapes", func(t *testing.T) {
		esc := env.run(ctx, "capture-screen", "-e", id)
		if !strings.Contains(esc, "\x1b[") {
			t.Fatalf("-e capture carries no escape sequence:\n%q", esc)
		}
		// The color survives as an attribute of its own cells: some SGR
		// sequence stands immediately before the text it paints.
		if !regexp.MustCompile("\x1b\\[[0-9;:]*mRED_MARKER").MatchString(esc) {
			t.Fatalf("-e capture does not color the red marker:\n%q", esc)
		}
		if got := strings.TrimSpace(stripANSI(esc)); got != plain {
			t.Fatalf("-e capture differs from the plain one once stripped:\n"+
				"got:\n%q\nwant:\n%q", got, plain)
		}
	})
}

// captureLineMarker matches the numbered lines the history test prints.
var captureLineMarker = regexp.MustCompile(`LINE_(\d{3})`)

// oldestCapturedLine returns the smallest LINE_NNN number in a capture, i.e.
// how far back that capture reached.
func oldestCapturedLine(t *testing.T, capture string) int {
	t.Helper()
	matches := captureLineMarker.FindAllStringSubmatch(capture, -1)
	if len(matches) == 0 {
		t.Fatalf("no LINE_NNN marker in capture:\n%s", capture)
	}
	oldest := math.MaxInt
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		must(t, err)
		oldest = min(oldest, n)
	}
	return oldest
}

// TestCaptureScreen_StartLineReachesHistory prints more lines than the screen
// holds, then verifies -S reaches the ones that scrolled off it.
func TestCaptureScreen_StartLineReachesHistory(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// 40 lines on a 24-row screen: the first ones are history by the end.
	script := `i=1
while [ $i -le 40 ]; do printf 'LINE_%03d\n' "$i"; i=$((i+1)); done
sleep 300`
	id := env.run(ctx, "run", "-t", "-n", "capture-history", "--", "/bin/sh", "-c", script)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	var plain string
	waitUntil(t, defaultTimeout, func() bool {
		out, _, err := env.exec(ctx, "capture-screen", id)
		if err != nil || !strings.Contains(out, "LINE_040") {
			return false
		}
		plain = out
		return true
	}, "capture-screen never showed the last printed line")

	if strings.Contains(plain, "LINE_001") {
		t.Fatalf("nothing scrolled off the screen, so -S has no history to reach:\n%s", plain)
	}

	// -S -5 prepends exactly the five history lines above the top visible row.
	back := env.run(ctx, "capture-screen", "-S", "-5", id)
	visibleTop, historyTop := oldestCapturedLine(t, plain), oldestCapturedLine(t, back)
	if visibleTop-historyTop != 5 {
		t.Fatalf("-S -5 reached back %d lines, want 5 (visible top LINE_%03d, "+
			"captured top LINE_%03d)", visibleTop-historyTop, visibleTop, historyTop)
	}

	// "-" is the extreme end: the whole history, back to the first line.
	whole := env.run(ctx, "capture-screen", "-S", "-", id)
	if !strings.Contains(whole, "LINE_001") {
		t.Fatalf("-S - did not reach the start of history:\n%s", whole)
	}
}

// TestCaptureScreen_NonTTYCommandIsRefused verifies a command running without a
// TTY is turned away with the verb that does serve it.
func TestCaptureScreen_NonTTYCommandIsRefused(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "capture-no-tty", "--",
		"/bin/sh", "-c", "echo NO_SCREEN; sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	stdout, stderr := env.runExpectFail(ctx, "capture-screen", id)
	combined := stdout + stderr
	if !strings.Contains(combined, "no terminal screen") {
		t.Fatalf("expected a TTY-only error; got:\n%s", combined)
	}
	if !strings.Contains(combined, "cmdman logs") {
		t.Fatalf("expected the error to point at cmdman logs; got:\n%s", combined)
	}
}

// TestCaptureScreen_StoppedCommandIsRefused verifies the screen is gone once the
// run that owned it ended.
func TestCaptureScreen_StoppedCommandIsRefused(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-t", "-n", "capture-stopped", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	env.run(ctx, "stop", id)
	env.waitForState(ctx, id, "exited", defaultTimeout)

	stdout, stderr := env.runExpectFail(ctx, "capture-screen", id)
	if combined := stdout + stderr; !strings.Contains(combined, "is not running") {
		t.Fatalf("expected a not-running error; got:\n%s", combined)
	}
}

// TestCaptureScreen_InvalidStartLineIsRejected verifies a malformed -S is
// reported as the flag it is. The value is parsed before any command is
// resolved, so the target need not exist.
func TestCaptureScreen_InvalidStartLineIsRejected(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	stdout, stderr := env.runExpectFail(ctx, "capture-screen", "-S", "top", "no-such-command")
	if combined := stdout + stderr; !strings.Contains(combined, "-S/--start-line") {
		t.Fatalf("expected the error to name the flag; got:\n%s", combined)
	}
}

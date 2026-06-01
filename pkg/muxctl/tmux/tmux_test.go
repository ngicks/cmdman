package tmux_test

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/pkg/muxctl"
	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

func TestNew_CreatesSessionAndWindow(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	if sess.WindowID() == "" {
		t.Fatal("WindowID is empty")
	}
	names := listWindowNames(t, socket, "cmdman-test")
	if !slices.Contains(names, "cmdman") {
		t.Errorf("window not created; have %v", names)
	}
}

func TestNew_FindsExistingWindow(t *testing.T) {
	requireTmux(t)
	sess1, socket := newSession(t, "cmdman")

	sess2, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:      socket,
		SessionName: "cmdman-test",
		WindowName:  "cmdman",
	})
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if sess1.WindowID() != sess2.WindowID() {
		t.Errorf("WindowID drifted on reuse: %s vs %s",
			sess1.WindowID(), sess2.WindowID())
	}
}

// TestNew_WindowIDBypassesFindOrCreate verifies that passing Config.WindowID
// targets the given window directly: SessionName/WindowName must be
// ignored, and no spurious window is created.
func TestNew_WindowIDBypassesFindOrCreate(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// Manually create the session + a window outside the driver, then
	// hand the resulting window id to tmux.New via Config.WindowID.
	run(t, socket, "new-session", "-d", "-s", "preexisting")
	wantID := run(t, socket, "new-window", "-d", "-t", "preexisting",
		"-n", "byid", "-P", "-F", "#{window_id}")

	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:   socket,
		WindowID: wantID,
	})
	if err != nil {
		t.Fatalf("New with WindowID: %v", err)
	}
	if sess.WindowID() != wantID {
		t.Errorf("WindowID = %q, want %q", sess.WindowID(), wantID)
	}

	// Sanity: no extra "cmdman" window was created behind our back.
	names := listWindowNames(t, socket, "preexisting")
	if slices.Contains(names, "cmdman") {
		t.Errorf("unexpected cmdman window created: %v", names)
	}

	// ApplyLayout works against the by-id session.
	root := loadLayout(t, "single-leaf.yaml", "")
	panes, err := sess.ApplyLayout(context.Background(), root, -1)
	if err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	if _, ok := panes["only"]; !ok {
		t.Errorf("missing 'only' pane: %v", sortedKeys(panes))
	}
}

func TestApplyLayout_SingleLeaf(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	root := loadLayout(t, "single-leaf.yaml", "")
	panes, err := sess.ApplyLayout(context.Background(), root, -1)
	if err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	if len(panes) != 1 {
		t.Errorf("want 1 pane, got %d", len(panes))
	}
	if _, ok := panes["only"]; !ok {
		t.Errorf("missing pane name 'only'; have %v", sortedKeys(panes))
	}
	if ids := listPaneIDs(t, socket, sess.WindowID()); len(ids) != 1 {
		t.Errorf("tmux reports %d panes, want 1", len(ids))
	}
}

func TestApplyLayout_HorizontalTwoLeaves(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	root := loadLayout(t, "horizontal-two.yaml", "")
	panes, err := sess.ApplyLayout(context.Background(), root, -1)
	if err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	if !slices.Equal(sortedKeys(panes), []string{"a", "b"}) {
		t.Errorf("pane names = %v, want [a b]", sortedKeys(panes))
	}
	if ids := listPaneIDs(t, socket, sess.WindowID()); len(ids) != 2 {
		t.Errorf("tmux reports %d panes, want 2", len(ids))
	}
	titles := listPaneTitles(t, socket, sess.WindowID())
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"a", "b"}) {
		t.Errorf("pane titles = %v, want [a b]", titles)
	}
}

func TestApplyLayout_NestedMixed(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	root := loadLayout(t, "nested-mixed.yaml", "")
	panes, err := sess.ApplyLayout(context.Background(), root, -1)
	if err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	want := []string{"api", "db", "redis", "worker"}
	if got := sortedKeys(panes); !slices.Equal(got, want) {
		t.Errorf("pane names = %v, want %v", got, want)
	}
	if ids := listPaneIDs(t, socket, sess.WindowID()); len(ids) != 4 {
		t.Errorf("tmux reports %d panes, want 4", len(ids))
	}

	// Focused pane = db (the only Focus:true leaf).
	active := run(t, socket, "display-message", "-t", sess.WindowID(),
		"-p", "#{pane_id}")
	if got := panes["db"].PaneId(); active != got {
		t.Errorf("active pane = %q, want db's id %q", active, got)
	}
}

func TestApplyLayout_ResetsOnReapply(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	first := loadLayout(t, "horizontal-three.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), first, -1); err != nil {
		t.Fatalf("first ApplyLayout: %v", err)
	}
	if got := len(listPaneIDs(t, socket, sess.WindowID())); got != 3 {
		t.Fatalf("after first apply, want 3 panes, got %d", got)
	}

	second := loadLayout(t, "single-leaf.yaml", "")
	panes, err := sess.ApplyLayout(context.Background(), second, -1)
	if err != nil {
		t.Fatalf("second ApplyLayout: %v", err)
	}
	if len(panes) != 1 {
		t.Errorf("after reset, want 1 pane in result map, got %d", len(panes))
	}
	if got := len(listPaneIDs(t, socket, sess.WindowID())); got != 1 {
		t.Errorf("after reset, tmux reports %d panes, want 1", got)
	}
}

func TestClose_KillsOnlyTheOwnedWindow(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	// Add an unrelated sibling window that Close must not touch.
	run(t, socket, "new-window", "-d", "-t", "cmdman-test", "-n", "user-window")

	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	names := listWindowNames(t, socket, "cmdman-test")
	if slices.Contains(names, "cmdman") {
		t.Errorf("owned window still present after Close: %v", names)
	}
	if !slices.Contains(names, "user-window") {
		t.Errorf("sibling window vanished after Close: %v", names)
	}
}

func TestApplyLayout_CmdOptTitleOverridesName(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	root := loadLayout(t, "cmdopt-title.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), root, -1); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	titles := listPaneTitles(t, socket, sess.WindowID())
	if !slices.Equal(titles, []string{"Pretty Title"}) {
		t.Errorf("titles = %v, want [Pretty Title]", titles)
	}
}

// TestApplyLayout_RecordsMarkerOption verifies that a non-negative marker is
// recorded on every pane via the @cmdman_marker per-pane option, while the
// pane border title carries only the plain pane name.
func TestApplyLayout_RecordsMarkerOption(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	root := loadLayout(t, "horizontal-two.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), root, 7); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	titles := listPaneTitles(t, socket, sess.WindowID())
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"a", "b"}) {
		t.Errorf("titles = %v, want [a b]", titles)
	}
	markers := listPaneMarkers(t, socket, sess.WindowID())
	slices.Sort(markers)
	if !slices.Equal(markers, []string{"7", "7"}) {
		t.Errorf("markers = %v, want [7 7]", markers)
	}
}

// TestApplyLayout_NegativeMarker_SkipsEmbed verifies that marker < 0
// leaves pane titles as plain base titles (no "#<digits>" suffix).
func TestApplyLayout_NegativeMarker_SkipsEmbed(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	root := loadLayout(t, "horizontal-two.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), root, -1); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	titles := listPaneTitles(t, socket, sess.WindowID())
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"a", "b"}) {
		t.Errorf("titles = %v, want [a b]", titles)
	}
}

// TestApplyLayout_DetachesViewersBeforeRebuild verifies that re-applying a
// layout first sends the detach-key sequence to the live, marker-bearing
// panes of the previous build — so a cmdman viewer gets a chance to exit
// cleanly instead of being SIGKILLed mid-frame by respawn-pane -k.
//
// The leaf stands in for a viewer: it puts its pty in raw mode (so ctrl-q is
// not swallowed as flow control), signals readiness, then blocks reading the
// 2-byte detach sequence and touches a sentinel on receipt. The sentinel is
// only created via the detach path; the prior teardown (kill/respawn) would
// SIGKILL the leaf before it ever read its stdin.
func TestApplyLayout_DetachesViewersBeforeRebuild(t *testing.T) {
	requireTmux(t)
	sess, _ := newSession(t, "cmdman")

	ready := tempPath(t, "ready")
	detached := tempPath(t, "detached")
	script := "stty raw -echo 2>/dev/null; : >" + ready +
		"; head -c 2 >/dev/null; : >" + detached
	root := muxctl.PaneSpec{
		Leaf: muxctl.Leaf{
			Name: "viewer",
			Cmd:  []string{"/bin/sh", "-c", script},
		},
	}

	if _, err := sess.ApplyLayout(context.Background(), root, 0); err != nil {
		t.Fatalf("first ApplyLayout: %v", err)
	}
	waitForMarker(t, sess, 0)
	if !waitForFile(ready, 3*time.Second) {
		t.Fatal("viewer never became ready")
	}

	if _, err := sess.ApplyLayout(context.Background(), root, 1); err != nil {
		t.Fatalf("second ApplyLayout: %v", err)
	}
	if !waitForFile(detached, 3*time.Second) {
		t.Fatal("viewer was not detached before rebuild (sentinel missing)")
	}
}

// TestApplyLayout_PreservesHashesInBaseTitle verifies that base titles
// (cmd_opt.title or leaf name) can contain '#' freely: storing the marker in
// a per-pane option (rather than a title suffix) keeps the title verbatim.
func TestApplyLayout_PreservesHashesInBaseTitle(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	// Build a tree by hand so we can use a base name containing '#'.
	root := muxctl.PaneSpec{
		Leaf: muxctl.Leaf{
			Name:   "weird",
			Cmd:    []string{"/bin/sh", "-c", "sleep 60"},
			CmdOpt: map[string]string{"title": "weird#name#5"},
		},
	}
	if _, err := sess.ApplyLayout(context.Background(), root, 3); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	titles := listPaneTitles(t, socket, sess.WindowID())
	if !slices.Equal(titles, []string{"weird#name#5"}) {
		t.Errorf("titles = %v, want [weird#name#5]", titles)
	}

	// StatWindow must round-trip: marker=3, name="weird#name#5".
	stat, err := sess.StatWindow(context.Background(), sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow: %v", err)
	}
	if stat.Marker != 3 {
		t.Errorf("Marker = %d, want 3", stat.Marker)
	}
	if !slices.Equal(stat.PaneNames, []string{"weird#name#5"}) {
		t.Errorf("PaneNames = %v, want [weird#name#5]", stat.PaneNames)
	}
}

func TestStatWindow_RoundTripsMarker(t *testing.T) {
	requireTmux(t)
	sess, _ := newSession(t, "cmdman")

	root := loadLayout(t, "nested-mixed.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), root, 2); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	stat, err := sess.StatWindow(context.Background(), sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow: %v", err)
	}
	if stat.Marker != 2 {
		t.Errorf("Marker = %d, want 2", stat.Marker)
	}
	got := append([]string(nil), stat.PaneNames...)
	slices.Sort(got)
	want := []string{"api", "db", "redis", "worker"}
	if !slices.Equal(got, want) {
		t.Errorf("PaneNames = %v, want %v", got, want)
	}
}

// TestStatWindow_NoMarker_ReturnsMinusOne verifies that a window whose
// panes carry no "#<digits>" suffix surfaces Marker = -1.
func TestStatWindow_NoMarker_ReturnsMinusOne(t *testing.T) {
	requireTmux(t)
	sess, _ := newSession(t, "cmdman")

	root := loadLayout(t, "horizontal-two.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), root, -1); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	stat, err := sess.StatWindow(context.Background(), sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow: %v", err)
	}
	if stat.Marker != -1 {
		t.Errorf("Marker = %d, want -1", stat.Marker)
	}
}

// TestStatWindow_InconsistentMarkers_ReturnsMinusOne verifies that
// panes carrying different markers surface as indeterminate (-1).
func TestStatWindow_InconsistentMarkers_ReturnsMinusOne(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	root := loadLayout(t, "horizontal-two.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), root, 1); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	// Manually rewrite one pane's marker option to a different value.
	ids := listPaneIDs(t, socket, sess.WindowID())
	if len(ids) < 2 {
		t.Fatalf("expected at least 2 panes, got %d", len(ids))
	}
	run(t, socket, "set-option", "-p", "-t", ids[0], "@cmdman_marker", "9")

	stat, err := sess.StatWindow(context.Background(), sess.WindowID())
	if err != nil {
		t.Fatalf("StatWindow: %v", err)
	}
	if stat.Marker != -1 {
		t.Errorf("Marker = %d, want -1 (inconsistent)", stat.Marker)
	}
}

// TestApplyLayout_SkipsTooSmall_WarnsViaContextLogger verifies that an
// over-budget layout (absolute size larger than the detached window's
// width) causes the leftover child to be skipped and a warning to be
// emitted via the context-scoped slog logger.
func TestApplyLayout_SkipsTooSmall_WarnsViaContextLogger(t *testing.T) {
	requireTmux(t)
	sess, _ := newSession(t, "cmdman")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	ctx := contextkey.WithSlogLogger(context.Background(), logger)

	// Detached tmux sessions default to 80x24. A 200-cell absolute leaves
	// nothing for the weighted siblings, so they are skipped.
	root := loadLayout(t, "oversized.yaml", "")
	panes, err := sess.ApplyLayout(ctx, root, -1)
	if err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	// "huge" gets built (absolutes are allowed to overflow); the two
	// weighted siblings collapse to 0 and only the trailing one is still
	// realized as the anchor.
	if _, ok := panes["huge"]; !ok {
		t.Errorf("huge pane missing from result: %v", sortedKeys(panes))
	}
	if _, ok := panes["dropped-a"]; ok {
		t.Errorf("dropped-a should have been skipped but is in result")
	}

	out := buf.String()
	if !strings.Contains(out, "window too small to fit layout") {
		t.Errorf("warning not found in log buffer; got:\n%s", out)
	}
	if !strings.Contains(out, "dropped-a") {
		t.Errorf("skipped pane name not in log; got:\n%s", out)
	}
}

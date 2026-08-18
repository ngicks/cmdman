package tmux_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/pkg/muxctl"
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

// TestNew_NeverAdoptsAWindowByName pins the driver's half of identity-keyed
// window lookup: a window name is a display label, so New builds a window of its
// own rather than moving into the same-named one that is already there. Adoption
// by name is what let two projects named alike — one repository checked out
// twice, brought up from either directory — fight over a single window, and the
// stamp the adopting call wrote would have orphaned the first project's window
// from its own down/cycle.
//
// Finding the window a caller already owns is that caller's job, via
// ListWindows filtered by identity; see cmdman/mux.Run for the find-or-create
// this leaves to the layer above.
func TestNew_NeverAdoptsAWindowByName(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	ctx := context.Background()
	srv := newServer(t, socket)
	theirs, err := srv.New(ctx, muxctl.Config{
		SessionName:   "cmdman-test",
		WindowName:    "cmdman-app",
		OwnedIdentity: "wd-a-app",
	})
	if err != nil {
		t.Fatalf("New (theirs): %v", err)
	}

	mine, err := srv.New(ctx, muxctl.Config{
		SessionName:   "cmdman-test",
		WindowName:    "cmdman-app", // the very same name
		OwnedIdentity: "wd-b-app",
	})
	if err != nil {
		t.Fatalf("New (mine): %v", err)
	}

	if mine.WindowID() == theirs.WindowID() {
		t.Fatalf("adopted the same-named window %s instead of creating one",
			theirs.WindowID())
	}
	if got := windowOwnerOption(t, socket, theirs.WindowID()); got != "wd-a-app" {
		t.Errorf("their ownership stamp = %q, want wd-a-app: the second New "+
			"must not restamp a window it did not build", got)
	}
	if got := windowOwnerOption(t, socket, mine.WindowID()); got != "wd-b-app" {
		t.Errorf("my ownership stamp = %q, want wd-b-app", got)
	}
	names := listWindowNames(t, socket, "cmdman-test")
	sameNamed := 0
	for _, n := range names {
		if n == "cmdman-app" {
			sameNamed++
		}
	}
	if sameNamed != 2 {
		t.Errorf("windows named cmdman-app = %d (%v), want the two New built", sameNamed, names)
	}
}

// TestNew_CreatesBesideAWindowNamedLikeTheSession guards the create target: the
// standalone dashboard names its window after its session ("cmdman" in
// "cmdman"), and tmux resolves a bare "-t <session>" against window names first,
// so a create that named only the session would resolve that window's index and
// fail with "index N in use". Nothing exercised it while New adopted same-named
// windows instead of building beside them.
func TestNew_CreatesBesideAWindowNamedLikeTheSession(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	ctx := context.Background()
	srv := newServer(t, socket)
	first, err := srv.New(ctx, muxctl.Config{SessionName: "cmdman", WindowName: "cmdman"})
	if err != nil {
		t.Fatalf("New (first): %v", err)
	}
	second, err := srv.New(ctx, muxctl.Config{SessionName: "cmdman", WindowName: "cmdman"})
	if err != nil {
		t.Fatalf("New (second): %v", err)
	}
	if first.WindowID() == second.WindowID() {
		t.Errorf("both News resolved %s; the second must build its own window",
			first.WindowID())
	}
}

// TestNew_StartDirectory covers Config.StartDirectory, which exists for the
// window a landing synthesizes for a project with no mux: section (D9): the
// shell has to open where the project lives, not wherever the tmux server was
// started. It applies to creation only — an existing window is never moved.
func TestNew_StartDirectory(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// t.TempDir() can sit behind a symlink (/tmp -> /private/tmp and the like)
	// while tmux reports the resolved path, so the expectation is resolved too.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	sess, err := newServer(t, socket).New(context.Background(), muxctl.Config{
		SessionName:    "cmdman-test",
		WindowName:     "cmdman-app",
		StartDirectory: dir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := run(t, socket, "list-panes", "-t", sess.WindowID(), "-F", "#{pane_current_path}")
	if got != dir {
		t.Errorf("pane_current_path = %q, want %q", got, dir)
	}
}

// TestNew_WindowIDBypassesCreate verifies that passing Config.WindowID
// targets the given window directly: SessionName/WindowName must be
// ignored, and no spurious window is created.
func TestNew_WindowIDBypassesCreate(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// Manually create the session + a window outside the driver, then
	// hand the resulting window id to tmux.New via Config.WindowID.
	run(t, socket, "new-session", "-d", "-s", "preexisting")
	wantID := run(t, socket, "new-window", "-d", "-t", "preexisting",
		"-n", "byid", "-P", "-F", "#{window_id}")

	sess, err := newServer(t, socket).New(context.Background(), muxctl.Config{
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

// TestNew_ReusesSinglePaneCurrentWindow verifies that ReuseCurrentWindow takes
// over the caller's current window when it has a single pane, instead of
// creating a separate named window.
func TestNew_ReusesSinglePaneCurrentWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	run(t, socket, "new-session", "-d", "-s", "main", "-n", "work")
	curID := run(t, socket, "display-message", "-t", "main:work", "-p", "#{window_id}")

	sess, err := newServer(t, socket).New(context.Background(), muxctl.Config{
		SessionName:        "main",
		WindowName:         "cmdman",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sess.WindowID() != curID {
		t.Errorf("expected to reuse current window %s, got %s", curID, sess.WindowID())
	}
	if names := listWindowNames(t, socket, "main"); slices.Contains(names, "cmdman") {
		t.Errorf("a separate cmdman window should not be created; have %v", names)
	}
}

// TestNew_DoesNotReuseMultiPaneCurrentWindow verifies that a multi-pane,
// non-muxctl current window is left alone and a fresh named window is built.
func TestNew_DoesNotReuseMultiPaneCurrentWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	run(t, socket, "new-session", "-d", "-s", "main", "-n", "work")
	run(t, socket, "split-window", "-t", "main:work")
	curID := run(t, socket, "display-message", "-t", "main:work", "-p", "#{window_id}")

	sess, err := newServer(t, socket).New(context.Background(), muxctl.Config{
		SessionName:        "main",
		WindowName:         "cmdman",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sess.WindowID() == curID {
		t.Errorf("multi-pane unowned window %s must not be reused", curID)
	}
	if names := listWindowNames(t, socket, "main"); !slices.Contains(names, "cmdman") {
		t.Errorf("expected a new cmdman window; have %v", names)
	}
}

// TestNew_ReusesOwnedCurrentWindow verifies that a window we built before —
// one that carries this caller's identity in the @cmdman_window ownership stamp
// — is reused in place even when it is multi-pane and its name does not match,
// so a re-run cycles the layout in place. Ownership is now determined by the
// window-level option rather than requiring every pane to carry a numeric
// marker; the test therefore builds the session with a non-empty OwnedIdentity
// and re-runs with the same one, as a real second `mux up` does.
func TestNew_ReusesOwnedCurrentWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// Build the initial session with an ownership stamp so currentWindowToReuse
	// recognises it via @cmdman_window regardless of pane count or name.
	srv := newServer(t, socket)
	sess, err := srv.New(context.Background(), muxctl.Config{
		SessionName:      "cmdman-test",
		WindowName:       "cmdman-owned",
		OwnedIdentity:    "my-project",
		ViewerDetachKeys: []string{"C-p", "C-q"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := loadLayout(t, "horizontal-two.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), root, 0); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	ownedID := sess.WindowID()
	// Make the owned window the session's current window.
	run(t, socket, "select-window", "-t", ownedID)

	sess2, err := srv.New(context.Background(), muxctl.Config{
		SessionName:        "cmdman-test",
		WindowName:         "unrelated-name",
		OwnedIdentity:      "my-project", // the same invocation re-run
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sess2.WindowID() != ownedID {
		t.Errorf("expected to reuse owned current window %s, got %s", ownedID, sess2.WindowID())
	}
}

// TestNew_DeclinesForeignIdentityCurrentWindow is the takeover guard: a plain
// `mux up` typed inside ANOTHER project's window must build its own window
// instead of eating that one. The foreign window is framed, which is where the
// old rule hurt most — it accepted any non-empty ownership stamp, and the apply
// that followed would have rebuilt the region under a project that is still
// running there.
func TestNew_DeclinesForeignIdentityCurrentWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	ctx := context.Background()

	srv := newServer(t, socket)
	foreign, err := srv.New(ctx, muxctl.Config{
		SessionName:   "main",
		WindowName:    "theirs",
		OwnedIdentity: "other-project",
	})
	if err != nil {
		t.Fatalf("New (foreign): %v", err)
	}
	if _, err := foreign.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 0); err != nil {
		t.Fatalf("ApplyLayout (foreign): %v", err)
	}
	frameID := dockFramePane(t, socket, foreign.WindowID())
	before := listPaneIDs(t, socket, foreign.WindowID())
	// Make their window the one the takeover check resolves as "current".
	run(t, socket, "select-window", "-t", foreign.WindowID())

	mine, err := srv.New(ctx, muxctl.Config{
		SessionName:        "main",
		WindowName:         "cmdman",
		OwnedIdentity:      "my-project",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("New (mine): %v", err)
	}
	if mine.WindowID() == foreign.WindowID() {
		t.Fatalf("took over window %s owned by another identity", foreign.WindowID())
	}
	if _, err := mine.ApplyLayout(ctx, loadLayout(t, "single-leaf.yaml", ""), 0); err != nil {
		t.Fatalf("ApplyLayout (mine): %v", err)
	}

	if got := listPaneIDs(t, socket, foreign.WindowID()); !slices.Equal(got, before) {
		t.Errorf("their panes = %v, want %v unchanged", got, before)
	}
	if got := paneFormat(t, socket, frameID, "#{@cmdman_frame}"); got == "" {
		t.Errorf("their frame pane %s lost its stamp", frameID)
	}
	if got := windowOption(
		t,
		socket,
		foreign.WindowID(),
		"@cmdman_window",
	); got != "other-project" {
		t.Errorf("their ownership stamp = %q, want other-project", got)
	}
	if names := listWindowNames(t, socket, "main"); !slices.Contains(names, "cmdman") {
		t.Errorf("expected a separate cmdman window; have %v", names)
	}
}

// TestNew_ReusesFrameOnlyCurrentWindow covers show-before-launch: a window that
// holds a frame and no project is chrome waiting for one, so a project started
// from inside it lands in the region the frame leaves over rather than opening
// somewhere else. Neither of the unowned rules would accept such a window — it
// is multi-pane and named nothing like ours — so this is its own branch.
func TestNew_ReusesFrameOnlyCurrentWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	ctx := context.Background()

	run(t, socket, "new-session", "-d", "-s", "main", "-n", "work")
	curID := run(t, socket, "display-message", "-t", "main:work", "-p", "#{window_id}")
	frameID := dockFramePane(t, socket, curID)
	run(t, socket, "set-option", "-w", "-t", curID, "@cmdman_frame_def", "dev")

	sess, err := newServer(t, socket).New(ctx, muxctl.Config{
		SessionName:        "main",
		WindowName:         "cmdman",
		OwnedIdentity:      "my-project",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sess.WindowID() != curID {
		t.Fatalf("expected to land in the framed window %s, got %s", curID, sess.WindowID())
	}
	if names := listWindowNames(t, socket, "main"); slices.Contains(names, "cmdman") {
		t.Errorf("a separate cmdman window should not be created; have %v", names)
	}

	if _, err := sess.ApplyLayout(ctx, loadLayout(t, "horizontal-two.yaml", ""), 0); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	if ids := listPaneIDs(t, socket, curID); !slices.Contains(ids, frameID) {
		t.Errorf("frame pane %s did not survive the project apply: %v", frameID, ids)
	}
	if got := windowOption(t, socket, curID, "@cmdman_window"); got != "my-project" {
		t.Errorf("ownership stamp = %q, want my-project", got)
	}
	if got := windowOption(t, socket, curID, "@cmdman_frame_def"); got != "dev" {
		t.Errorf("frame def = %q, want dev: the project must not clear it", got)
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

package tmux_test

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/frame"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// windowOption returns the window-scoped value of a tmux option (via -v),
// tolerating errors by returning "" — an unset option is exactly what some of
// these assertions check for.
func windowOption(t *testing.T, socket, windowID, name string) string {
	t.Helper()
	out, err := exec.Command(
		requireTmux(t), "-L", socket,
		"show-options", "-w", "-t", windowID, "-v", name,
	).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// cmdmanWindowOptions returns the window's set user options in the @cmdman_
// namespace, one "name value" line each, in tmux's own order. Every piece of
// window-level state the driver installs lives under that prefix, so an empty
// result is the direct reading of "no residual state".
func cmdmanWindowOptions(t *testing.T, socket, windowID string) []string {
	t.Helper()
	var got []string
	for line := range strings.SplitSeq(run(t, socket, "show-options", "-w", "-t", windowID), "\n") {
		if strings.HasPrefix(line, "@cmdman_") {
			got = append(got, line)
		}
	}
	return got
}

// listPaneStamps returns each pane's three cmdman stamps concatenated, in
// tmux's list order: an empty entry is a pane carrying none of them.
func listPaneStamps(t *testing.T, socket, windowID string) []string {
	t.Helper()
	out := run(
		t, socket, "list-panes", "-t", windowID,
		"-F", "#{@cmdman_frame}#{@cmdman_marker}#{@cmdman_leaf}",
	)
	return strings.Split(out, "\n")
}

// TestOpen_ReturnsFalseWhenNoWindow verifies Open is a no-op
// signal (ok=false, no Session) when the named window does not exist — so a
// teardown caller never spawns a stray window.
func TestOpen_ReturnsFalseWhenNoWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// A session exists, but no window named "cmdman".
	run(t, socket, "new-session", "-d", "-s", "cmdman-test", "-n", "work")

	sess, ok, err := newServer(t, socket).Open(context.Background(), muxctl.Config{
		SessionName: "cmdman-test",
		WindowName:  "cmdman",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ok || sess != nil {
		t.Fatalf("expected ok=false/sess=nil for absent window, got ok=%v sess=%v", ok, sess)
	}
}

// TestOpen_FindsNamedWindow verifies Open locates the dedicated
// named window a prior New built.
func TestOpen_FindsNamedWindow(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	got, ok, err := newServer(t, socket).Open(context.Background(), muxctl.Config{
		SessionName: "cmdman-test",
		WindowName:  "cmdman",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !ok {
		t.Fatal("expected to find the named window")
	}
	if got.WindowID() != sess.WindowID() {
		t.Errorf("WindowID = %q, want %q", got.WindowID(), sess.WindowID())
	}
}

// TestOpen_FindsOwnedCurrentWindow verifies the reuse-current case: a
// dashboard built into a window whose NAME differs from the owned name is found
// via the owned-current path (find-by-name "cmdman" would not match "work").
// Ownership is the @cmdman_window option and a teardown caller must claim the
// identity it tears down, so the build and the Open name the same one.
func TestOpen_FindsOwnedCurrentWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	srv := newServer(t, socket)
	sess, err := srv.New(context.Background(), muxctl.Config{
		SessionName:      "main",
		WindowName:       "work",
		OwnedIdentity:    "find-owned-test",
		ViewerDetachKeys: []string{"C-p", "C-q"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := sess.ApplyLayout(
		context.Background(), loadLayout(t, "single-leaf.yaml", ""), 0,
	); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	// Make the owned dashboard the session's current window.
	run(t, socket, "select-window", "-t", sess.WindowID())

	got, ok, err := srv.Open(context.Background(), muxctl.Config{
		SessionName:        "main",
		WindowName:         "cmdman", // deliberately NOT "work"
		OwnedIdentity:      "find-owned-test",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !ok {
		t.Fatal("expected to find the owned current window")
	}
	if got.WindowID() != sess.WindowID() {
		t.Errorf("WindowID = %q, want %q", got.WindowID(), sess.WindowID())
	}
}

// TestOpen_RejectsUnmarkedSinglePaneCurrent is the key safety case that
// distinguishes Open from New: New would TAKE OVER an unmarked
// single-pane current window (its single-pane reuse rule), but a teardown must
// never repurpose an arbitrary window the user happens to be sitting in.
func TestOpen_RejectsUnmarkedSinglePaneCurrent(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// A plain single-pane window — the kind New's ReuseCurrentWindow accepts.
	run(t, socket, "new-session", "-d", "-s", "main", "-n", "work")

	_, ok, err := newServer(t, socket).Open(context.Background(), muxctl.Config{
		SessionName:        "main",
		WindowName:         "cmdman",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if ok {
		t.Fatal("Open must NOT take over an unmarked single-pane current window")
	}
}

// TestOpen_RejectsForeignAndFrameOnlyCurrent pins the other half of the takeover
// guard, on the teardown side: sitting in a window is not a claim to it. Another
// project's dashboard would be collapsed by the Detach that follows, and a
// window holding only a frame was never this caller's to open — the frame side
// finds its windows through the frame slot [Server.ListWindows] reports, not by
// guessing from the current window.
func TestOpen_RejectsForeignAndFrameOnlyCurrent(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, socket string) string // returns the window to make current
	}{
		{
			name: "owned by another identity",
			setup: func(t *testing.T, socket string) string {
				t.Helper()
				sess, err := newServer(t, socket).New(ctx, muxctl.Config{
					SessionName:   "main",
					WindowName:    "theirs",
					OwnedIdentity: "other-project",
				})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return sess.WindowID()
			},
		},
		{
			name: "framed with no project",
			setup: func(t *testing.T, socket string) string {
				t.Helper()
				run(t, socket, "new-session", "-d", "-s", "main", "-n", "work")
				wid := run(t, socket, "display-message", "-t", "main:work", "-p", "#{window_id}")
				dockFramePane(t, socket, wid)
				run(t, socket, "set-option", "-w", "-t", wid, "@cmdman_frame_def", "dev")
				return wid
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := uniqueSocket(t)
			t.Cleanup(func() { killServer(t, socket) })

			wid := tc.setup(t, socket)
			run(t, socket, "select-window", "-t", wid)

			_, ok, err := newServer(t, socket).Open(ctx, muxctl.Config{
				SessionName:        "main",
				WindowName:         "cmdman", // no window by this name exists
				OwnedIdentity:      "my-project",
				ReuseCurrentWindow: true,
			})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if ok {
				t.Fatalf("Open must not accept the current window (%s)", wid)
			}
		})
	}
}

// TestDetach_CollapsesWindowToSingleCleanPane verifies Detach restores the
// window: one pane, the per-pane marker cleared, and the window-level
// pane-border-status no longer "top". The window itself survives (Detach is not
// Close).
func TestDetach_CollapsesWindowToSingleCleanPane(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	root := loadLayout(t, "horizontal-two.yaml", "")
	if _, err := sess.ApplyLayout(context.Background(), root, 7); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	// Preconditions: two marked panes, pane-border-status enabled.
	if got := len(listPaneIDs(t, socket, sess.WindowID())); got != 2 {
		t.Fatalf("want 2 panes before detach, got %d", got)
	}
	if got := windowOption(t, socket, sess.WindowID(), "pane-border-status"); got != "top" {
		t.Fatalf("pane-border-status before detach = %q, want top", got)
	}

	if err := sess.Detach(context.Background()); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	if got := len(listPaneIDs(t, socket, sess.WindowID())); got != 1 {
		t.Fatalf("want 1 pane after detach, got %d", got)
	}
	for _, m := range listPaneMarkers(t, socket, sess.WindowID()) {
		if m != "" {
			t.Errorf("marker not cleared after detach: %q", m)
		}
	}
	if got := windowOption(t, socket, sess.WindowID(), "pane-border-status"); got == "top" {
		t.Errorf("pane-border-status still %q after detach; want it unset", got)
	}
	if names := listWindowNames(t, socket, "cmdman-test"); !slices.Contains(names, "cmdman") {
		t.Errorf("owned window vanished after detach (should be restored, not killed): %v", names)
	}
}

// TestDetach_GracefullyDetachesViewers mirrors
// TestApplyLayout_DetachesViewersBeforeRebuild but for Detach: the in-pane
// viewer must receive the detach key sequence (and exit cleanly) before the
// window is torn down, rather than being SIGKILLed mid-frame. The leaf puts its
// pty in raw mode, signals readiness, blocks reading the 2-byte detach
// sequence, then touches a sentinel — only reachable via the detach path.
func TestDetach_GracefullyDetachesViewers(t *testing.T) {
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
		t.Fatalf("ApplyLayout: %v", err)
	}
	waitForMarker(t, sess, 0)
	if !waitForFile(ready, 3*time.Second) {
		t.Fatal("viewer never became ready")
	}

	if err := sess.Detach(context.Background()); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if !waitForFile(detached, 3*time.Second) {
		t.Fatal("viewer was not detached before teardown (sentinel missing)")
	}
}

// TestDetach_SiblingWindowUntouched verifies Detach only restores the owned
// window and leaves unrelated sibling windows alone.
func TestDetach_SiblingWindowUntouched(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	if _, err := sess.ApplyLayout(
		context.Background(), loadLayout(t, "single-leaf.yaml", ""), 0,
	); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	run(t, socket, "new-window", "-d", "-t", "cmdman-test", "-n", "user-window")

	if err := sess.Detach(context.Background()); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	names := listWindowNames(t, socket, "cmdman-test")
	if !slices.Contains(names, "cmdman") {
		t.Errorf("owned window vanished after detach: %v", names)
	}
	if !slices.Contains(names, "user-window") {
		t.Errorf("sibling window vanished after detach: %v", names)
	}
}

// framedSession builds an owned window holding a two-pane project layout with a
// frame docked above it — the window both teardowns act on. It returns the
// session, the socket, and the ids of the project and frame panes.
func framedSession(
	t *testing.T,
	identity string,
	marker int,
) (sess muxctl.Session, socket string, project, framed []string) {
	t.Helper()
	socket = uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	ctx := context.Background()
	sess, err := newServer(t, socket).New(ctx, muxctl.Config{
		SessionName:      "cmdman-test",
		WindowName:       "cmdman",
		OwnedIdentity:    identity,
		ViewerDetachKeys: []string{"C-p", "C-q"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := sess.ApplyLayout(
		ctx,
		loadLayout(t, "horizontal-two.yaml", ""),
		marker,
	); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	project = listPaneIDs(t, socket, sess.WindowID())
	if err := sess.ShowFrame(ctx, carveFrame(t, frame.Entry{
		Edge:    frame.EdgeTop,
		Size:    frame.Size{N: 3}.MuxSize(),
		Command: sleepArgv,
	}), mainPaneName, "dev"); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}
	return sess, socket, project, framePaneIDs(t, socket, sess.WindowID())
}

// TestDetach_OnAFramedWindowSparesTheFrame pins the project half of per-side
// teardown: `mux down` on a framed window gives the project's region back and
// touches nothing else. The frame's panes keep running, its window state stays
// put, and so does the pane-border row its titles are drawn in — a window
// stamped for neither side is the failure the split exists to prevent.
func TestDetach_OnAFramedWindowSparesTheFrame(t *testing.T) {
	requireTmux(t)
	const identity = "framed-detach"
	sess, socket, project, framed := framedSession(t, identity, 7)
	if len(framed) != 1 {
		t.Fatalf("frame panes before detach = %v, want the def's one entry", framed)
	}
	framePIDs := panePIDs(t, socket, framed)

	if err := sess.Detach(context.Background()); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	if got := framePaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, framed) {
		t.Errorf("frame panes after detach = %v, want the untouched %v", got, framed)
	}
	assertPanesAlive(t, socket, framed, framePIDs, "by the project teardown")
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q after detach, want the frame still recorded", got)
	}
	if got := windowOption(t, socket, sess.WindowID(), "pane-border-status"); got != "top" {
		t.Errorf("pane-border-status = %q after detach, want top: the frame draws titles", got)
	}

	// The project side, and only it, is gone: its stamp cleared and its region
	// collapsed to the one default pane a frame shown before any launch frames.
	if got := windowOwnerOption(t, socket, sess.WindowID()); got != "" {
		t.Errorf("@cmdman_window = %q after detach, want empty", got)
	}
	ids := listPaneIDs(t, socket, sess.WindowID())
	if len(ids) != len(framed)+1 {
		t.Errorf("panes after detach = %v, want the frame's %v plus one default pane", ids, framed)
	}
	for _, id := range ids {
		if slices.Contains(framed, id) {
			continue
		}
		if got := paneFormat(t, socket, id, "#{@cmdman_marker}"); got != "" {
			t.Errorf("project pane %s kept marker %q after detach", id, got)
		}
		if !slices.Contains(project, id) {
			t.Errorf("pane %s is neither a frame pane nor one of the project's %v", id, project)
		}
	}
}

// TestDetach_OnAFramedWindowWithNoProjectPanes is the project-side mirror of the
// frame-only hide: the project's panes are gone — their viewers exited, and the
// frame is what kept the window alive — while its stamp is still on the window.
// Detach is the only call that clears that stamp, so failing for want of a
// region to collapse would strand the window claimed by a project that has
// nothing left in it.
func TestDetach_OnAFramedWindowWithNoProjectPanes(t *testing.T) {
	requireTmux(t)
	const identity = "framed-detach-empty"
	sess, socket, project, framed := framedSession(t, identity, 7)
	for _, id := range project {
		run(t, socket, "kill-pane", "-t", id)
	}
	if got := listPaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, framed) {
		t.Fatalf("panes after killing the project = %v, want the frame's %v", got, framed)
	}

	if err := sess.Detach(context.Background()); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	if got := windowOwnerOption(t, socket, sess.WindowID()); got != "" {
		t.Errorf("@cmdman_window = %q after detach, want empty", got)
	}
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_scale"); got != "" {
		t.Errorf("@cmdman_scale = %q after detach, want empty", got)
	}
	if got := framePaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, framed) {
		t.Errorf("frame panes after detach = %v, want the untouched %v", got, framed)
	}
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q after detach, want the frame still recorded", got)
	}
}

// TestHideFrame_SparesTheProject is the mirror image: the frame side clears its
// own record and leaves the project's — stamp, markers, border row — exactly as
// it found them.
func TestHideFrame_SparesTheProject(t *testing.T) {
	requireTmux(t)
	const identity = "framed-hide"
	sess, socket, project, framed := framedSession(t, identity, 7)
	projectPIDs := panePIDs(t, socket, project)

	if err := sess.HideFrame(context.Background()); err != nil {
		t.Fatalf("HideFrame: %v", err)
	}

	if got := listPaneIDs(t, socket, sess.WindowID()); !slices.Equal(got, project) {
		t.Errorf("panes after hide = %v, want the project's %v", got, project)
	}
	assertPanesAlive(t, socket, project, projectPIDs, "by the frame teardown")
	if got := windowOwnerOption(t, socket, sess.WindowID()); got != identity {
		t.Errorf("@cmdman_window = %q after hide, want %q", got, identity)
	}
	if got := windowOption(t, socket, sess.WindowID(), "pane-border-status"); got != "top" {
		t.Errorf("pane-border-status = %q after hide, want top: the project is still up", got)
	}
	for _, m := range listPaneMarkers(t, socket, sess.WindowID()) {
		if m != "7" {
			t.Errorf("project marker = %q after hide, want the applied 7", m)
		}
	}
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_frame_def"); got != "" {
		t.Errorf("@cmdman_frame_def = %q after hide, want it cleared", got)
	}
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 0 {
		t.Errorf("frame panes after hide = %v, want the %v it showed gone", got, framed)
	}
}

// assertWindowRestored asserts the window is back to a plain one: it still
// exists, holds a single unstamped pane, and carries no cmdman state at all.
func assertWindowRestored(t *testing.T, sess muxctl.Session, socket string) {
	t.Helper()
	if names := listWindowNames(t, socket, "cmdman-test"); !slices.Contains(names, "cmdman") {
		t.Fatalf("window was closed rather than restored: %v", names)
	}
	if got := listPaneIDs(t, socket, sess.WindowID()); len(got) != 1 {
		t.Errorf("panes after the last teardown = %v, want one", got)
	}
	for _, stamp := range listPaneStamps(t, socket, sess.WindowID()) {
		if stamp != "" {
			t.Errorf("pane still carries a cmdman stamp (%q) after the last teardown", stamp)
		}
	}
	if got := cmdmanWindowOptions(t, socket, sess.WindowID()); len(got) != 0 {
		t.Errorf("residual window state after the last teardown: %v", got)
	}
	if got := windowOption(t, socket, sess.WindowID(), "pane-border-status"); got == "top" {
		t.Errorf("pane-border-status still %q after the last teardown, want it unset", got)
	}
}

// TestTeardown_TheLastSideRestoresTheWindow pins F8 from both directions:
// whichever teardown removes the last cmdman state performs the full restore,
// so the window a user gets back is the same one either way round.
func TestTeardown_TheLastSideRestoresTheWindow(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()

	t.Run("frame goes last", func(t *testing.T) {
		sess, socket, _, _ := framedSession(t, "frame-last", 2)
		if err := sess.Detach(ctx); err != nil {
			t.Fatalf("Detach: %v", err)
		}
		if err := sess.HideFrame(ctx); err != nil {
			t.Fatalf("HideFrame: %v", err)
		}
		assertWindowRestored(t, sess, socket)
	})

	t.Run("project goes last", func(t *testing.T) {
		sess, socket, _, _ := framedSession(t, "project-last", 2)
		if err := sess.HideFrame(ctx); err != nil {
			t.Fatalf("HideFrame: %v", err)
		}
		if err := sess.Detach(ctx); err != nil {
			t.Fatalf("Detach: %v", err)
		}
		assertWindowRestored(t, sess, socket)
	})
}

// TestHideFrame_OnAFrameOnlyWindowLeavesADefaultPane covers the window a frame
// is all that is left of: shown before any launch, its main pane exited under
// the user. Killing every frame pane would close the window outright, so the
// last one becomes the default pane the restore would have respawned anyway —
// the frame verbs can always undo themselves.
func TestHideFrame_OnAFrameOnlyWindowLeavesADefaultPane(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	ctx := context.Background()

	main := listPaneIDs(t, socket, sess.WindowID())
	if err := sess.ShowFrame(ctx, carveFrame(t,
		frame.Entry{Edge: frame.EdgeTop, Size: frame.Size{N: 3}.MuxSize(), Command: sleepArgv},
		frame.Entry{Edge: frame.EdgeLeft, Size: frame.Size{N: 20}.MuxSize(), Command: sleepArgv},
	), mainPaneName, "dev"); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}
	// The main region goes away under the frame, the way a shell the user typed
	// "exit" into does.
	run(t, socket, "kill-pane", "-t", main[0])
	if got := framePaneIDs(t, socket, sess.WindowID()); len(got) != 2 {
		t.Fatalf("frame panes = %v, want the window to hold nothing else", got)
	}

	if err := sess.HideFrame(ctx); err != nil {
		t.Fatalf("HideFrame: %v", err)
	}
	assertWindowRestored(t, sess, socket)
	left := listPaneIDs(t, socket, sess.WindowID())
	if got := paneFormat(t, socket, left[0], "#{pane_title}"); got != "" {
		t.Errorf("pane title after hide = %q, want the frame leaf's title cleared", got)
	}
}

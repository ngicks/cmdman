package tmux_test

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/muxctl"
	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
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

// TestOpenExisting_ReturnsFalseWhenNoWindow verifies OpenExisting is a no-op
// signal (ok=false, no Session) when the named window does not exist — so a
// teardown caller never spawns a stray window.
func TestOpenExisting_ReturnsFalseWhenNoWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// A session exists, but no window named "cmdman".
	run(t, socket, "new-session", "-d", "-s", "cmdman-test", "-n", "work")

	sess, ok, err := tmuxctl.OpenExisting(context.Background(), tmuxctl.Config{
		Socket:      socket,
		SessionName: "cmdman-test",
		WindowName:  "cmdman",
	})
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	if ok || sess != nil {
		t.Fatalf("expected ok=false/sess=nil for absent window, got ok=%v sess=%v", ok, sess)
	}
}

// TestOpenExisting_FindsNamedWindow verifies OpenExisting locates the dedicated
// named window a prior New built.
func TestOpenExisting_FindsNamedWindow(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	got, ok, err := tmuxctl.OpenExisting(context.Background(), tmuxctl.Config{
		Socket:      socket,
		SessionName: "cmdman-test",
		WindowName:  "cmdman",
	})
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	if !ok {
		t.Fatal("expected to find the named window")
	}
	if got.WindowID() != sess.WindowID() {
		t.Errorf("WindowID = %q, want %q", got.WindowID(), sess.WindowID())
	}
}

// TestOpenExisting_FindsMarkedCurrentWindow verifies the reuse-current case: a
// dashboard built into a window whose NAME differs from the owned name is found
// via the marked-current path (find-by-name "cmdman" would not match "work").
func TestOpenExisting_FindsMarkedCurrentWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:           socket,
		SessionName:      "main",
		WindowName:       "work",
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
	// Make the marked dashboard the session's current window.
	run(t, socket, "select-window", "-t", sess.WindowID())

	got, ok, err := tmuxctl.OpenExisting(context.Background(), tmuxctl.Config{
		Socket:             socket,
		SessionName:        "main",
		WindowName:         "cmdman", // deliberately NOT "work"
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	if !ok {
		t.Fatal("expected to find the marked current window")
	}
	if got.WindowID() != sess.WindowID() {
		t.Errorf("WindowID = %q, want %q", got.WindowID(), sess.WindowID())
	}
}

// TestOpenExisting_RejectsUnmarkedSinglePaneCurrent is the key safety case that
// distinguishes OpenExisting from New: New would TAKE OVER an unmarked
// single-pane current window (its single-pane reuse rule), but a teardown must
// never repurpose an arbitrary window the user happens to be sitting in.
func TestOpenExisting_RejectsUnmarkedSinglePaneCurrent(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// A plain single-pane window — the kind New's ReuseCurrentWindow accepts.
	run(t, socket, "new-session", "-d", "-s", "main", "-n", "work")

	_, ok, err := tmuxctl.OpenExisting(context.Background(), tmuxctl.Config{
		Socket:             socket,
		SessionName:        "main",
		WindowName:         "cmdman",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	if ok {
		t.Fatal("OpenExisting must NOT take over an unmarked single-pane current window")
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

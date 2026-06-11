package tmux_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// windowOwnerOption reads the @cmdman_window window-level option for windowID,
// returning "" when the option is unset (show-options exits non-zero for
// absent options). Used throughout ownership assertions.
func windowOwnerOption(t *testing.T, socket, windowID string) string {
	t.Helper()
	out, err := exec.Command(
		requireTmux(t), "-L", socket,
		"show-options", "-w", "-t", windowID, "-v", "@cmdman_window",
	).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestNew_StampsOwnerOption_FindOrCreate verifies that New stamps
// @cmdman_window on the window when OwnedIdentity is set, using the
// find-or-create path (Config.WindowID empty, ReuseCurrentWindow false).
// This is the primary stamping path exercised from outside tmux or from
// a context where display-message client resolution is unavailable.
func TestNew_StampsOwnerOption_FindOrCreate(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "test-project-abc123"
	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		SessionName:   "stamp-test",
		WindowName:    "cmdman",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := windowOwnerOption(t, socket, sess.WindowID())
	if got != identity {
		t.Errorf("@cmdman_window = %q, want %q", got, identity)
	}
}

// TestNew_NoStampWhenIdentityEmpty verifies that New leaves @cmdman_window
// unset when OwnedIdentity is empty — callers that do not need enumeration
// (one-off builds, tests) should not litter options.
func TestNew_NoStampWhenIdentityEmpty(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		SessionName:   "no-stamp-test",
		WindowName:    "cmdman",
		OwnedIdentity: "", // deliberately empty
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := windowOwnerOption(t, socket, sess.WindowID())
	if got != "" {
		t.Errorf("@cmdman_window = %q, want empty when OwnedIdentity is unset", got)
	}
}

// TestNew_StampsOwnerOption_WindowIDPath verifies that New stamps @cmdman_window
// when a window is targeted directly via Config.WindowID — this covers the path
// that would be taken by a takeover window that has already been resolved by
// the caller and handed in via WindowID.
//
// NOTE on ReuseCurrentWindow / display-message path: currentWindowToReuse calls
// "tmux display-message" which resolves the CURRENT window of an ATTACHED
// client. Without a real attached tmux client (which cannot be driven in a
// headless test context), display-message returns an empty or error result and
// currentWindowToReuse returns ok=false — causing New to fall back to
// find-or-create. The stamping itself is not path-specific (it runs after wid
// is resolved regardless of how wid was obtained), so the WindowID path below
// is sufficient to verify that the stamp block in New is reachable and correct.
// An integration test with a real attached client would be required to exercise
// the display-message takeover path end-to-end.
func TestNew_StampsOwnerOption_WindowIDPath(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// Pre-create the window outside the driver, then pass its id via WindowID.
	run(t, socket, "new-session", "-d", "-s", "wid-test")
	wantID := run(t, socket, "new-window", "-d", "-t", "wid-test",
		"-n", "mywindow", "-P", "-F", "#{window_id}")

	const identity = "wid-path-identity"
	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		WindowID:      wantID,
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New with WindowID: %v", err)
	}
	if sess.WindowID() != wantID {
		t.Fatalf("WindowID = %q, want %q", sess.WindowID(), wantID)
	}

	got := windowOwnerOption(t, socket, wantID)
	if got != identity {
		t.Errorf("@cmdman_window = %q, want %q", got, identity)
	}
}

// TestDetach_ClearsOwnerOption verifies that Detach unsets @cmdman_window so
// the restored window is no longer enumerable as a cmdman-owned window.
// It extends the detach suite in detach_test.go.
func TestDetach_ClearsOwnerOption(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "detach-clear-test"
	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		SessionName:   "detach-test",
		WindowName:    "cmdman",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Pre-condition: stamp is present.
	if got := windowOwnerOption(t, socket, sess.WindowID()); got != identity {
		t.Fatalf("precondition: @cmdman_window = %q, want %q", got, identity)
	}

	if err := sess.Detach(context.Background()); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Post-condition: stamp is gone.
	if got := windowOwnerOption(t, socket, sess.WindowID()); got != "" {
		t.Errorf("@cmdman_window = %q after Detach, want empty", got)
	}
}

// TestListOwnedWindows_TwoSessionsTwoIdentities builds two sessions on one
// socket, one dashboard window each with different identities (one window
// renamed after stamping to simulate a takeover window that kept its original
// name), and asserts:
//
//   - Server-wide scan finds both with correct fields (SessionName, WindowName,
//     Identity, WindowID).
//   - Identity filter returns only the matching row.
//   - Session filter restricts results to the named session.
//   - A non-existent session filter returns empty rows and no error.
func TestListOwnedWindows_TwoSessionsTwoIdentities(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identA = "project-alpha"
	const identB = "project-beta"

	// Session A — window named "dash-a", stamped with identA.
	sessA, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		SessionName:   "session-a",
		WindowName:    "dash-a",
		OwnedIdentity: identA,
	})
	if err != nil {
		t.Fatalf("New session-a: %v", err)
	}

	// Session B — window initially named "original", stamped with identB, then
	// renamed to simulate a takeover window (the window keeps its pre-takeover
	// name while the identity stamp tracks the true owner).
	sessB, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		SessionName:   "session-b",
		WindowName:    "original",
		OwnedIdentity: identB,
	})
	if err != nil {
		t.Fatalf("New session-b: %v", err)
	}
	// Rename the window after stamping: the identity survives the rename.
	run(t, socket, "rename-window", "-t", sessB.WindowID(), "renamed-after-stamp")

	// ── server-wide scan ─────────────────────────────────────────────────────

	all, err := tmuxctl.ListOwnedWindows(context.Background(), tmuxctl.ListOwnedWindowsOptions{
		Socket: socket,
	})
	if err != nil {
		t.Fatalf("ListOwnedWindows (server-wide): %v", err)
	}

	// Build a map by identity for easy assertions.
	byIdentity := make(map[string]tmuxctl.OwnedWindow)
	for _, row := range all {
		byIdentity[row.Identity] = row
	}

	rowA, ok := byIdentity[identA]
	if !ok {
		t.Fatalf("identity %q not found in server-wide results; got %v", identA, all)
	}
	if rowA.SessionName != "session-a" {
		t.Errorf("identA.SessionName = %q, want session-a", rowA.SessionName)
	}
	if rowA.WindowID != sessA.WindowID() {
		t.Errorf("identA.WindowID = %q, want %q", rowA.WindowID, sessA.WindowID())
	}
	if rowA.WindowName != "dash-a" {
		t.Errorf("identA.WindowName = %q, want dash-a", rowA.WindowName)
	}

	rowB, ok := byIdentity[identB]
	if !ok {
		t.Fatalf("identity %q not found in server-wide results; got %v", identB, all)
	}
	if rowB.SessionName != "session-b" {
		t.Errorf("identB.SessionName = %q, want session-b", rowB.SessionName)
	}
	if rowB.WindowID != sessB.WindowID() {
		t.Errorf("identB.WindowID = %q, want %q", rowB.WindowID, sessB.WindowID())
	}
	// The window was renamed after stamping; WindowName reflects the current name.
	if rowB.WindowName != "renamed-after-stamp" {
		t.Errorf("identB.WindowName = %q, want renamed-after-stamp", rowB.WindowName)
	}

	// ── identity filter ───────────────────────────────────────────────────────

	filtered, err := tmuxctl.ListOwnedWindows(context.Background(), tmuxctl.ListOwnedWindowsOptions{
		Socket:   socket,
		Identity: identA,
	})
	if err != nil {
		t.Fatalf("ListOwnedWindows (identity filter): %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("identity filter: want 1 row, got %d: %v", len(filtered), filtered)
	}
	if filtered[0].Identity != identA {
		t.Errorf("filtered row identity = %q, want %q", filtered[0].Identity, identA)
	}

	// ── session filter ────────────────────────────────────────────────────────

	inSessionA, err := tmuxctl.ListOwnedWindows(
		context.Background(),
		tmuxctl.ListOwnedWindowsOptions{
			Socket:  socket,
			Session: "session-a",
		},
	)
	if err != nil {
		t.Fatalf("ListOwnedWindows (session filter): %v", err)
	}
	if len(inSessionA) != 1 {
		t.Fatalf(
			"session filter: want 1 row for session-a, got %d: %v",
			len(inSessionA),
			inSessionA,
		)
	}
	if inSessionA[0].Identity != identA {
		t.Errorf("session-a row identity = %q, want %q", inSessionA[0].Identity, identA)
	}

	// ── nonexistent session → empty, no error ─────────────────────────────────

	gone, err := tmuxctl.ListOwnedWindows(context.Background(), tmuxctl.ListOwnedWindowsOptions{
		Socket:  socket,
		Session: "does-not-exist",
	})
	if err != nil {
		t.Fatalf("ListOwnedWindows (nonexistent session): want nil error, got %v", err)
	}
	if len(gone) != 0 {
		t.Errorf("nonexistent session: want 0 rows, got %d: %v", len(gone), gone)
	}
}

// TestOwnership_SurvivesExtraUnmarkedPane stamps a window, then manually
// splits an extra pane (simulating a user adding a pane to the dashboard
// window), and asserts that:
//
//   - ListOwnedWindows still returns the window (ownership is window-level,
//     not per-pane, so pane churn cannot break it).
//   - OpenExisting via WindowID still resolves the window.
//   - Detach still collapses the window to a single clean pane.
//
// This is the key regression guard for the old all-panes-marked check, which
// failed as soon as the user manually opened a pane in the dashboard window.
func TestOwnership_SurvivesExtraUnmarkedPane(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "survive-extra-pane"
	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		SessionName:   "extra-pane-test",
		WindowName:    "cmdman",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Apply a layout so the window has marked panes.
	if _, err := sess.ApplyLayout(
		context.Background(), loadLayout(t, "single-leaf.yaml", ""), 1,
	); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}

	// Manually split an extra pane — the user's simulated intervention.
	run(t, socket, "split-window", "-t", sess.WindowID())

	panes := listPaneIDs(t, socket, sess.WindowID())
	if len(panes) != 2 {
		t.Fatalf("expected 2 panes after manual split, got %d", len(panes))
	}

	// ── ListOwnedWindows still finds the window ───────────────────────────────

	rows, err := tmuxctl.ListOwnedWindows(context.Background(), tmuxctl.ListOwnedWindowsOptions{
		Socket:   socket,
		Identity: identity,
	})
	if err != nil {
		t.Fatalf("ListOwnedWindows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 owned window, got %d: %v", len(rows), rows)
	}
	if rows[0].WindowID != sess.WindowID() {
		t.Errorf("WindowID = %q, want %q", rows[0].WindowID, sess.WindowID())
	}

	// ── OpenExisting via WindowID still resolves ──────────────────────────────

	reopened, ok, err := tmuxctl.OpenExisting(context.Background(), tmuxctl.Config{
		Socket:   socket,
		WindowID: sess.WindowID(),
	})
	if err != nil {
		t.Fatalf("OpenExisting: %v", err)
	}
	if !ok || reopened == nil {
		t.Fatal("OpenExisting returned ok=false after extra-pane split")
	}
	if reopened.WindowID() != sess.WindowID() {
		t.Errorf("reopened WindowID = %q, want %q", reopened.WindowID(), sess.WindowID())
	}

	// ── Detach still collapses to a single clean pane ─────────────────────────

	if err := sess.Detach(context.Background()); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if got := len(listPaneIDs(t, socket, sess.WindowID())); got != 1 {
		t.Errorf("want 1 pane after Detach, got %d", got)
	}
	// Ownership stamp cleared.
	if got := windowOwnerOption(t, socket, sess.WindowID()); got != "" {
		t.Errorf("@cmdman_window = %q after Detach, want empty", got)
	}
}

// TestListOwnedWindows_NeverStartedSocket verifies that querying a socket
// that has never had a server started returns an empty slice and no error.
// This covers the deployment-time case where cmdman asks "any dashboards up?"
// before the user has ever run tmux.
func TestListOwnedWindows_NeverStartedSocket(t *testing.T) {
	requireTmux(t)
	// uniqueSocket produces a name that no test has used — no killServer needed
	// because the server was never started.
	socket := uniqueSocket(t) + "-never-started"

	rows, err := tmuxctl.ListOwnedWindows(context.Background(), tmuxctl.ListOwnedWindowsOptions{
		Socket: socket,
	})
	if err != nil {
		t.Fatalf("ListOwnedWindows against never-started socket: want nil error, got %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("want 0 rows, got %d: %v", len(rows), rows)
	}
}

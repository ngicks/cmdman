package tmux_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/frame"
	"github.com/ngicks/cmdman/pkg/muxctl"
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
	sess, err := newServer(t, socket).New(context.Background(), muxctl.Config{
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

	sess, err := newServer(t, socket).New(context.Background(), muxctl.Config{
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
// "tmux display-message", which resolves the current window of the server's
// current session — a select-window is enough to steer it, so the takeover
// rules themselves are covered headlessly (see TestNew_ReusesOwnedCurrentWindow
// and the takeover-guard cases beside it). What still needs a real attached
// client is only tmux's client-dependent resolution when several clients sit in
// different windows. The stamping below is not path-specific either (it runs
// after wid is resolved, however it was obtained), so the WindowID path is what
// this test keeps honest.
func TestNew_StampsOwnerOption_WindowIDPath(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// Pre-create the window outside the driver, then pass its id via WindowID.
	run(t, socket, "new-session", "-d", "-s", "wid-test")
	wantID := run(t, socket, "new-window", "-d", "-t", "wid-test",
		"-n", "mywindow", "-P", "-F", "#{window_id}")

	const identity = "wid-path-identity"
	sess, err := newServer(t, socket).New(context.Background(), muxctl.Config{
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
	sess, err := newServer(t, socket).New(context.Background(), muxctl.Config{
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

// TestListWindows_TwoSessionsTwoIdentities builds two sessions on one
// socket, one dashboard window each with different identities (one window
// renamed after stamping to simulate a takeover window that kept its original
// name), and asserts:
//
//   - Server-wide scan finds both with correct fields (SessionName, WindowName,
//     Identity, WindowID).
//   - Identity filter returns only the matching row.
//   - Session filter restricts results to the named session.
//   - A non-existent session filter returns empty rows and no error.
func TestListWindows_TwoSessionsTwoIdentities(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identA = "project-alpha"
	const identB = "project-beta"

	srv := newServer(t, socket)

	// Session A — window named "dash-a", stamped with identA.
	sessA, err := srv.New(context.Background(), muxctl.Config{
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
	sessB, err := srv.New(context.Background(), muxctl.Config{
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

	all, err := srv.ListWindows(context.Background(), muxctl.ListOptions{})
	if err != nil {
		t.Fatalf("ListWindows (server-wide): %v", err)
	}

	// Build a map by identity for easy assertions.
	byIdentity := make(map[string]muxctl.Window)
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

	filtered, err := srv.ListWindows(context.Background(), muxctl.ListOptions{
		Identity: identA,
	})
	if err != nil {
		t.Fatalf("ListWindows (identity filter): %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("identity filter: want 1 row, got %d: %v", len(filtered), filtered)
	}
	if filtered[0].Identity != identA {
		t.Errorf("filtered row identity = %q, want %q", filtered[0].Identity, identA)
	}

	// ── session filter ────────────────────────────────────────────────────────

	inSessionA, err := srv.ListWindows(
		context.Background(),
		muxctl.ListOptions{
			Session: "session-a",
		},
	)
	if err != nil {
		t.Fatalf("ListWindows (session filter): %v", err)
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

	gone, err := srv.ListWindows(context.Background(), muxctl.ListOptions{
		Session: "does-not-exist",
	})
	if err != nil {
		t.Fatalf("ListWindows (nonexistent session): want nil error, got %v", err)
	}
	if len(gone) != 0 {
		t.Errorf("nonexistent session: want 0 rows, got %d: %v", len(gone), gone)
	}
}

// TestOwnership_SurvivesExtraUnmarkedPane stamps a window, then manually
// splits an extra pane (simulating a user adding a pane to the dashboard
// window), and asserts that:
//
//   - ListWindows still returns the window (ownership is window-level,
//     not per-pane, so pane churn cannot break it).
//   - Open via WindowID still resolves the window.
//   - Detach still collapses the window to a single clean pane.
//
// This is the key regression guard for the old all-panes-marked check, which
// failed as soon as the user manually opened a pane in the dashboard window.
func TestOwnership_SurvivesExtraUnmarkedPane(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "survive-extra-pane"
	srv := newServer(t, socket)
	sess, err := srv.New(context.Background(), muxctl.Config{
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

	// ── ListWindows still finds the window ───────────────────────────────

	rows, err := srv.ListWindows(context.Background(), muxctl.ListOptions{
		Identity: identity,
	})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 owned window, got %d: %v", len(rows), rows)
	}
	if rows[0].WindowID != sess.WindowID() {
		t.Errorf("WindowID = %q, want %q", rows[0].WindowID, sess.WindowID())
	}

	// ── Open via WindowID still resolves ──────────────────────────────

	reopened, ok, err := srv.Open(context.Background(), muxctl.Config{
		WindowID: sess.WindowID(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !ok || reopened == nil {
		t.Fatal("Open returned ok=false after extra-pane split")
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

// showFrameOn shows a one-entry frame named defName around whatever windowID's
// session holds, through the session the caller already has.
func showFrameOn(t *testing.T, sess muxctl.Session, defName string) {
	t.Helper()
	root := carveFrame(t, frame.Entry{
		Edge:    frame.EdgeTop,
		Size:    frame.Size{N: 3}.MuxSize(),
		Command: sleepArgv,
	})
	if err := sess.ShowFrame(context.Background(), root, mainPaneName, defName); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}
}

// TestListWindows_ReportsBothIdentities pins identity coexistence: a framed
// window answers for its project exactly as an unframed one does — the frame
// never took the ownership slot — while the frame it holds rides along on the
// same row. The second window is the case the enumeration gate has to widen
// for: framed with no project (all a `mux down` under a frame leaves behind),
// discoverable server-wide, and never an answer to another project's query.
func TestListWindows_ReportsBothIdentities(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "project-alpha"
	ctx := context.Background()
	srv := newServer(t, socket)

	framed, err := srv.New(ctx, muxctl.Config{
		SessionName:   "session-a",
		WindowName:    "dash-a",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New session-a: %v", err)
	}
	showFrameOn(t, framed, "dev")

	// No OwnedIdentity: the frame is the only thing this window carries.
	frameOnly, err := srv.New(ctx, muxctl.Config{
		SessionName: "session-b",
		WindowName:  "dash-b",
	})
	if err != nil {
		t.Fatalf("New session-b: %v", err)
	}
	showFrameOn(t, frameOnly, "ops")

	all, err := srv.ListWindows(ctx, muxctl.ListOptions{})
	if err != nil {
		t.Fatalf("ListWindows (server-wide): %v", err)
	}
	byWindow := make(map[string]muxctl.Window, len(all))
	for _, row := range all {
		byWindow[row.WindowID] = row
	}

	row, ok := byWindow[framed.WindowID()]
	if !ok {
		t.Fatalf("framed window %s missing from the scan: %v", framed.WindowID(), all)
	}
	if row.Identity != identity {
		t.Errorf("framed row Identity = %q, want %q", row.Identity, identity)
	}
	if row.Frame != "dev" {
		t.Errorf("framed row Frame = %q, want dev", row.Frame)
	}

	row, ok = byWindow[frameOnly.WindowID()]
	if !ok {
		t.Fatalf("frame-only window %s missing from the scan: %v", frameOnly.WindowID(), all)
	}
	if row.Identity != "" {
		t.Errorf("frame-only row Identity = %q, want empty", row.Identity)
	}
	if row.Frame != "ops" {
		t.Errorf("frame-only row Frame = %q, want ops", row.Frame)
	}

	// ── the project's own query still finds its window, framed and all ────────

	mine, err := srv.ListWindows(ctx, muxctl.ListOptions{Identity: identity})
	if err != nil {
		t.Fatalf("ListWindows (identity filter): %v", err)
	}
	if len(mine) != 1 || mine[0].WindowID != framed.WindowID() {
		t.Fatalf("identity %q matched %v, want only the framed window %s",
			identity, mine, framed.WindowID())
	}
	if mine[0].Frame != "dev" {
		t.Errorf("filtered row Frame = %q, want dev", mine[0].Frame)
	}

	// ── and a frame name is never an answer for a project ─────────────────────

	byFrameName, err := srv.ListWindows(ctx, muxctl.ListOptions{Identity: "ops"})
	if err != nil {
		t.Fatalf("ListWindows (frame name as identity): %v", err)
	}
	if len(byFrameName) != 0 {
		t.Errorf("identity %q matched %v; the frame slot must not answer for a project",
			"ops", byFrameName)
	}
}

// TestListWindows_InlineStateSurvivesTheFrameField guards the row parsing: the
// frame def became a base field ahead of the inline state values, so a state
// key must still read back from its own column — on a framed window and on an
// unframed one, where tmux drops the trailing empty field entirely.
func TestListWindows_InlineStateSurvivesTheFrameField(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	ctx := context.Background()
	srv := newServer(t, socket)
	for _, tc := range []struct{ session, identity, frameDef, scale string }{
		{"session-framed", "with-frame", "dev", "web=2"},
		{"session-plain", "no-frame", "", "worker=3"},
	} {
		sess, err := srv.New(ctx, muxctl.Config{
			SessionName:   tc.session,
			WindowName:    "dash",
			OwnedIdentity: tc.identity,
		})
		if err != nil {
			t.Fatalf("New %s: %v", tc.session, err)
		}
		if tc.frameDef != "" {
			showFrameOn(t, sess, tc.frameDef)
		}
		if err := srv.WriteWindowState(
			ctx, sess.WindowID(), muxctl.StateKeyScale, tc.scale,
		); err != nil {
			t.Fatalf("WriteWindowState %s: %v", tc.session, err)
		}

		rows, err := srv.ListWindows(ctx, muxctl.ListOptions{
			Identity:  tc.identity,
			StateKeys: []muxctl.StateKey{muxctl.StateKeyScale},
		})
		if err != nil {
			t.Fatalf("ListWindows %s: %v", tc.session, err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s: want 1 row, got %v", tc.session, rows)
		}
		if rows[0].Frame != tc.frameDef {
			t.Errorf("%s: Frame = %q, want %q", tc.session, rows[0].Frame, tc.frameDef)
		}
		if got := rows[0].State[muxctl.StateKeyScale]; got != tc.scale {
			t.Errorf("%s: scale state = %q, want %q", tc.session, got, tc.scale)
		}
	}
}

// TestListWindows_NeverStartedSocket verifies that querying a socket
// that has never had a server started returns an empty slice and no error.
// This covers the deployment-time case where cmdman asks "any dashboards up?"
// before the user has ever run tmux.
func TestListWindows_NeverStartedSocket(t *testing.T) {
	requireTmux(t)
	// uniqueSocket produces a name that no test has used — no killServer needed
	// because the server was never started.
	socket := uniqueSocket(t) + "-never-started"

	rows, err := newServer(t, socket).ListWindows(context.Background(), muxctl.ListOptions{})
	if err != nil {
		t.Fatalf("ListWindows against never-started socket: want nil error, got %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("want 0 rows, got %d: %v", len(rows), rows)
	}
}

package tmux_test

import (
	"context"
	"slices"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// windowCreatedOption reads the @cmdman_created window option for windowID,
// returning "" when it is unset (show-options exits non-zero for an absent
// option).
func windowCreatedOption(t *testing.T, socket, windowID string) string {
	t.Helper()
	return windowOption(t, socket, windowID, "@cmdman_created")
}

// TestNew_StampsCreatedOnlyOnTheWindowsItBuilt pins which of New's three
// resolutions leaves the created marker behind. Only the window New opened is
// cmdman's to close later; the other two were handed over by the caller, and a
// teardown that closed one would take the shell the user was running in it.
func TestNew_StampsCreatedOnlyOnTheWindowsItBuilt(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	ctx := context.Background()
	srv := newServer(t, socket)

	// The window the user is sitting in, taken over in place: single-pane and
	// unowned, so ReuseCurrentWindow accepts it.
	run(t, socket, "new-session", "-d", "-s", "main", "-n", "work")
	borrowedID := run(t, socket, "display-message", "-t", "main:work", "-p", "#{window_id}")
	borrowed, err := srv.New(ctx, muxctl.Config{
		SessionName:        "main",
		WindowName:         "cmdman",
		OwnedIdentity:      "borrowed-project",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("New (takeover): %v", err)
	}
	if borrowed.WindowID() != borrowedID {
		t.Fatalf("expected the takeover of %s, got %s", borrowedID, borrowed.WindowID())
	}

	// A window the caller resolved itself and handed in by id.
	handedID := run(t, socket, "new-window", "-d", "-a", "-t", "=main:{end}",
		"-n", "theirs", "-P", "-F", "#{window_id}")
	if _, err := srv.New(ctx, muxctl.Config{
		WindowID:      handedID,
		OwnedIdentity: "handed-project",
	}); err != nil {
		t.Fatalf("New (WindowID): %v", err)
	}

	// The window cmdman opened for itself.
	created, err := srv.New(ctx, muxctl.Config{
		SessionName:   "main",
		WindowName:    "dash",
		OwnedIdentity: "created-project",
	})
	if err != nil {
		t.Fatalf("New (create): %v", err)
	}

	if got := windowCreatedOption(t, socket, created.WindowID()); got != "1" {
		t.Errorf("@cmdman_created on the window New opened = %q, want 1", got)
	}
	if got := windowCreatedOption(t, socket, borrowedID); got != "" {
		t.Errorf("@cmdman_created on the taken-over window = %q, want empty", got)
	}
	if got := windowCreatedOption(t, socket, handedID); got != "" {
		t.Errorf("@cmdman_created on the handed-in window = %q, want empty", got)
	}

	// ── and the listing reports it ────────────────────────────────────────────

	rows, err := srv.ListWindows(ctx, muxctl.ListOptions{})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	byWindow := make(map[string]muxctl.Window, len(rows))
	for _, row := range rows {
		byWindow[row.WindowID] = row
	}
	for _, tc := range []struct {
		what string
		id   string
		want bool
	}{
		{"the window New opened", created.WindowID(), true},
		{"the taken-over window", borrowedID, false},
		{"the handed-in window", handedID, false},
	} {
		row, ok := byWindow[tc.id]
		if !ok {
			t.Fatalf("%s (%s) missing from the scan: %v", tc.what, tc.id, rows)
		}
		if row.Created != tc.want {
			t.Errorf("%s: Created = %v, want %v", tc.what, row.Created, tc.want)
		}
	}
}

// TestListWindows_CreatedDoesNotDisplaceTheStateColumns guards the row parsing:
// the created marker became a sixth base field ahead of the inline state
// values, so a state key must still read back from its own column — including
// on a borrowed window, where tmux drops the empty marker field entirely.
func TestListWindows_CreatedDoesNotDisplaceTheStateColumns(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	ctx := context.Background()
	srv := newServer(t, socket)

	run(t, socket, "new-session", "-d", "-s", "main", "-n", "work")
	borrowed, err := srv.New(ctx, muxctl.Config{
		SessionName:        "main",
		WindowName:         "cmdman",
		OwnedIdentity:      "borrowed-project",
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("New (takeover): %v", err)
	}
	created, err := srv.New(ctx, muxctl.Config{
		SessionName:   "main",
		WindowName:    "dash",
		OwnedIdentity: "created-project",
	})
	if err != nil {
		t.Fatalf("New (create): %v", err)
	}

	for _, tc := range []struct {
		identity string
		sess     muxctl.Session
		scale    string
		created  bool
	}{
		{"borrowed-project", borrowed, "web=2", false},
		{"created-project", created, "worker=3", true},
	} {
		if err := srv.WriteWindowState(
			ctx, tc.sess.WindowID(), muxctl.StateKeyScale, tc.scale,
		); err != nil {
			t.Fatalf("WriteWindowState %s: %v", tc.identity, err)
		}
		rows, err := srv.ListWindows(ctx, muxctl.ListOptions{
			Identity:  tc.identity,
			StateKeys: []muxctl.StateKey{muxctl.StateKeyScale},
		})
		if err != nil {
			t.Fatalf("ListWindows %s: %v", tc.identity, err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s: want 1 row, got %v", tc.identity, rows)
		}
		if rows[0].Created != tc.created {
			t.Errorf("%s: Created = %v, want %v", tc.identity, rows[0].Created, tc.created)
		}
		if got := rows[0].State[muxctl.StateKeyScale]; got != tc.scale {
			t.Errorf("%s: scale state = %q, want %q", tc.identity, got, tc.scale)
		}
	}
}

// TestCreatedStampOutlivesOneSideAndDiesWithTheWindow pins when the marker is
// cleared. It records where the window came from, not who is in it, so a
// project teardown under a frame must leave it alone — the window is still
// cmdman's, and a later teardown may still close it. Once the last side is gone
// the window is nobody's: the marker goes with the rest of the state, or a
// takeover of that same window would read as one cmdman built.
func TestCreatedStampOutlivesOneSideAndDiesWithTheWindow(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	ctx := context.Background()
	srv := newServer(t, socket)

	sess, err := srv.New(ctx, muxctl.Config{
		SessionName:   "cmdman-test",
		WindowName:    "cmdman",
		OwnedIdentity: "framed-project",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	showFrameOn(t, sess, "dev")

	if err := sess.Detach(ctx); err != nil {
		t.Fatalf("Detach (project side): %v", err)
	}
	if got := windowCreatedOption(t, socket, sess.WindowID()); got != "1" {
		t.Errorf("@cmdman_created = %q while the frame is still up, want 1", got)
	}

	if err := sess.HideFrame(ctx); err != nil {
		t.Fatalf("HideFrame (last side): %v", err)
	}
	if got := windowCreatedOption(t, socket, sess.WindowID()); got != "" {
		t.Errorf("@cmdman_created = %q after the last teardown, want it cleared", got)
	}
	// The marker must not keep the window captive either: the border row the
	// window was given back is what says the restore actually completed.
	if got := windowOption(t, socket, sess.WindowID(), "pane-border-status"); got == "top" {
		t.Errorf("pane-border-status still %q after the last teardown, want it unset", got)
	}
	if names := listWindowNames(t, socket, "cmdman-test"); !slices.Contains(names, "cmdman") {
		t.Errorf("window was closed rather than restored: %v", names)
	}
}

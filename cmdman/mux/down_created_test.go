package mux

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// TestDownKillCreatedClosesOnlyWhatCmdmanOpened puts both kinds of window a
// project can end up in under one identity — one cmdman opened, one it borrowed
// by taking over the window the caller was sitting in — and tears them down in
// a single call. The one cmdman opened goes away entirely; the borrowed one is
// handed back with its shell intact, because closing it would end the shell the
// user was running there.
func TestDownKillCreatedClosesOnlyWhatCmdmanOpened(t *testing.T) {
	bin := tmuxOrSkip(t)
	socket := scratchServer(t, "borrowed")
	ctx := context.Background()
	driver := muxctl.DriverSpec{Name: "tmux", Socket: socket}

	const identity = "one-project"
	server, err := resolveServer(ctx, driver, []string{})
	if err != nil {
		t.Fatal(err)
	}

	// scratchServer's session opens with a single unowned pane — the window a
	// user is sitting in, which the takeover rules accept.
	borrowedID := tmuxRun(t, bin, socket, "display-message", "-t", "borrowed", "-p", "#{window_id}")
	borrowed, err := server.New(ctx, muxctl.Config{
		SessionName:        "borrowed",
		WindowName:         "cmdman",
		OwnedIdentity:      identity,
		ReuseCurrentWindow: true,
	})
	if err != nil {
		t.Fatalf("New (takeover): %v", err)
	}
	if borrowed.WindowID() != borrowedID {
		t.Fatalf("expected the takeover of %s, got %s", borrowedID, borrowed.WindowID())
	}

	created, err := server.New(ctx, muxctl.Config{
		SessionName:   "borrowed",
		WindowName:    "cmdman-dash",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New (create): %v", err)
	}
	createdID := created.WindowID()

	var printed strings.Builder
	if err := Down(ctx, DownOptions{
		Driver:      driver,
		Identity:    identity,
		KillCreated: true,
		Env:         []string{},
		Stdout:      &printed,
	}); err != nil {
		t.Fatalf("Down: %v", err)
	}

	ids := tmuxRun(t, bin, socket, "list-windows", "-a", "-F", "#{window_id}")
	remaining := strings.Split(ids, "\n")
	if slices.Contains(remaining, createdID) {
		t.Errorf("window %s cmdman opened survived the teardown: %v", createdID, remaining)
	}
	if !slices.Contains(remaining, borrowedID) {
		t.Fatalf("borrowed window %s was closed; the user's shell went with it", borrowedID)
	}

	// The borrowed one was restored, not merely left alone: its ownership stamp
	// is gone, so the next `mux up` no longer recognises it.
	rows, err := List(ctx, ListOptions{Driver: driver, Identity: identity, Env: []string{}})
	if err != nil {
		t.Fatalf("List after Down: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("windows still answering for %q after the teardown: %v", identity, rows)
	}

	note := printed.String()
	if !strings.Contains(note, "Removed window cmdman-dash ("+createdID+")") {
		t.Errorf("Down said %q; want it to report removing %s", note, createdID)
	}
	if !strings.Contains(note, "Restored window") || !strings.Contains(note, borrowedID) {
		t.Errorf("Down said %q; want it to report restoring %s", note, borrowedID)
	}
}

// TestDownKillCreatedRestoresTheWindowItRunsIn covers the caller that is inside
// what it is tearing down — the switcher docked in a dashboard's frame, taking
// down the project whose window displays it. Closing that window would SIGHUP
// the teardown mid-way, so the window cmdman created is restored instead, and
// the teardown lives to say so.
//
// The pane is handed over the way tmux hands it to what it starts, through the
// environment, so no process has to be running in it for the guard to be under
// test.
func TestDownKillCreatedRestoresTheWindowItRunsIn(t *testing.T) {
	bin := tmuxOrSkip(t)
	socket := scratchServer(t, "hosting")
	ctx := context.Background()
	driver := muxctl.DriverSpec{Name: "tmux", Socket: socket}

	const identity = "hosting-project"
	server, err := resolveServer(ctx, driver, []string{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := server.New(ctx, muxctl.Config{
		SessionName:   "hosting",
		WindowName:    "cmdman-dash",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New (create): %v", err)
	}
	createdID := created.WindowID()
	hostingPane := tmuxRun(
		t, bin, socket, "list-panes", "-t", createdID, "-F", "#{pane_id}",
	)

	var printed strings.Builder
	if err := Down(ctx, DownOptions{
		Driver:      driver,
		Identity:    identity,
		KillCreated: true,
		Env:         []string{"TMUX_PANE=" + hostingPane},
		Stdout:      &printed,
	}); err != nil {
		t.Fatalf("Down: %v", err)
	}

	ids := tmuxRun(t, bin, socket, "list-windows", "-a", "-F", "#{window_id}")
	if !slices.Contains(strings.Split(ids, "\n"), createdID) {
		t.Fatalf("window %s was closed underneath the teardown running in it", createdID)
	}

	// Restored, not merely left alone: the ownership stamp is gone, so the
	// project is down as far as anything looking for it is concerned.
	rows, err := List(ctx, ListOptions{Driver: driver, Identity: identity, Env: []string{}})
	if err != nil {
		t.Fatalf("List after Down: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("windows still answering for %q after the teardown: %v", identity, rows)
	}
	if note := printed.String(); !strings.Contains(note, "Restored window") ||
		!strings.Contains(note, createdID) {
		t.Errorf("Down said %q; want it to report restoring %s", note, createdID)
	}
}

// TestDownWithoutKillCreatedRestoresEverything is the command line's teardown:
// with the option off, a window cmdman opened is emptied like any other rather
// than closed underneath the prompt the user typed at.
func TestDownWithoutKillCreatedRestoresEverything(t *testing.T) {
	bin := tmuxOrSkip(t)
	socket := scratchServer(t, "restore")
	ctx := context.Background()
	driver := muxctl.DriverSpec{Name: "tmux", Socket: socket}

	const identity = "restore-project"
	server, err := resolveServer(ctx, driver, []string{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := server.New(ctx, muxctl.Config{
		SessionName:   "restore",
		WindowName:    "cmdman-dash",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New (create): %v", err)
	}

	var printed strings.Builder
	if err := Down(ctx, DownOptions{
		Driver:   driver,
		Identity: identity,
		Env:      []string{},
		Stdout:   &printed,
	}); err != nil {
		t.Fatalf("Down: %v", err)
	}

	ids := tmuxRun(t, bin, socket, "list-windows", "-a", "-F", "#{window_id}")
	if !slices.Contains(strings.Split(ids, "\n"), created.WindowID()) {
		t.Errorf("window %s was closed; the default teardown restores", created.WindowID())
	}
	if note := printed.String(); !strings.Contains(note, "Restored window") {
		t.Errorf("Down said %q, want a restore line", note)
	}
}

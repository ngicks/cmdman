package tmux_test

import (
	"context"
	"testing"
)

// TestCurrentSessionName_NotAttached covers the not-inside-tmux path: a socket
// whose server was never started makes display-message fail, which
// CurrentSessionName reports as ok=false with a nil error — the tolerance the
// mux layer relied on when it shelled out to tmux directly for the same probe.
func TestCurrentSessionName_NotAttached(t *testing.T) {
	requireTmux(t)
	// A never-used socket name: no server to connect to, no cleanup needed.
	socket := uniqueSocket(t) + "-never-started"

	name, ok, err := newServer(t, socket).CurrentSessionName(context.Background())
	if err != nil {
		t.Fatalf("CurrentSessionName: want nil error, got %v", err)
	}
	if ok || name != "" {
		t.Errorf("want ok=false/name=\"\" for never-started socket, got ok=%v name=%q", ok, name)
	}
}

// TestCurrentSessionName_Attached covers the inside-tmux path: with a running
// server holding a single session, display-message resolves that session's name
// even without a live attached client, so CurrentSessionName round-trips it with
// ok=true.
func TestCurrentSessionName_Attached(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	run(t, socket, "new-session", "-d", "-s", "inside-sess")

	name, ok, err := newServer(t, socket).CurrentSessionName(context.Background())
	if err != nil {
		t.Fatalf("CurrentSessionName: %v", err)
	}
	if !ok {
		t.Fatal("want ok=true with a running session")
	}
	if name != "inside-sess" {
		t.Errorf("session name = %q, want inside-sess", name)
	}
}

// TestCurrentWindowID_NotStarted covers the no-server path of the window half
// of the "where is the caller" probe: nothing to ask means ok=false with a nil
// error, exactly as CurrentSessionName reports it.
func TestCurrentWindowID_NotStarted(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t) + "-never-started"

	id, ok, err := newServer(t, socket).CurrentWindowID(context.Background(), "")
	if err != nil {
		t.Fatalf("CurrentWindowID: want nil error, got %v", err)
	}
	if ok || id != "" {
		t.Errorf("want ok=false/id=\"\" for a never-started socket, got ok=%v id=%q", ok, id)
	}
}

// TestCurrentWindowID_Session pins what the frame verbs address a window by:
// the named session's CURRENT window, not its first — a per-window fixture goes
// around the window the user is looking at.
func TestCurrentWindowID_Session(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	run(t, socket, "new-session", "-d", "-s", "inside-sess")
	second := run(t, socket, "new-window", "-d", "-t", "inside-sess", "-P", "-F", "#{window_id}")
	run(t, socket, "select-window", "-t", second)

	server := newServer(t, socket)
	id, ok, err := server.CurrentWindowID(context.Background(), "inside-sess")
	if err != nil {
		t.Fatalf("CurrentWindowID: %v", err)
	}
	if !ok {
		t.Fatal("want ok=true for a running session")
	}
	if id != second {
		t.Errorf("window id = %q, want the session's current window %q", id, second)
	}

	t.Run("unknown session", func(t *testing.T) {
		id, ok, err := server.CurrentWindowID(context.Background(), "no-such-session")
		if err != nil {
			t.Fatalf("CurrentWindowID: want nil error, got %v", err)
		}
		if ok || id != "" {
			t.Errorf("want ok=false/id=\"\" for an unknown session, got ok=%v id=%q", ok, id)
		}
	})
}

// TestEnclosingWindowID is the process-relative half of "where is the caller",
// and the distinction from CurrentWindowID is the point: the pane named in the
// environment is in the session's non-current window, so a client-relative
// answer would name the other one.
func TestEnclosingWindowID(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	run(t, socket, "new-session", "-d", "-s", "enclosing")
	background := run(t, socket, "new-window", "-d", "-t", "enclosing", "-P", "-F", "#{window_id}")
	pane := run(t, socket, "list-panes", "-t", background, "-F", "#{pane_id}")

	server := newServer(t, socket)
	id, ok, err := server.EnclosingWindowID(context.Background(), []string{"TMUX_PANE=" + pane})
	if err != nil {
		t.Fatalf("EnclosingWindowID: %v", err)
	}
	if !ok {
		t.Fatal("want ok=true for a live pane")
	}
	if id != background {
		t.Errorf("window id = %q, want the pane's own window %q", id, background)
	}

	t.Run("no pane in the environment", func(t *testing.T) {
		// What a display-popup child is handed: inside tmux, but in no pane.
		id, ok, err := server.EnclosingWindowID(context.Background(), []string{"TMUX=" + socket})
		if err != nil {
			t.Fatalf("EnclosingWindowID: want nil error, got %v", err)
		}
		if ok || id != "" {
			t.Errorf("want ok=false/id=\"\" with no pane named, got ok=%v id=%q", ok, id)
		}
	})

	t.Run("pane this server does not know", func(t *testing.T) {
		id, ok, err := server.EnclosingWindowID(context.Background(), []string{"TMUX_PANE=%9999"})
		if err != nil {
			t.Fatalf("EnclosingWindowID: want nil error, got %v", err)
		}
		if ok || id != "" {
			t.Errorf("want ok=false/id=\"\" for an unknown pane, got ok=%v id=%q", ok, id)
		}
	})
}

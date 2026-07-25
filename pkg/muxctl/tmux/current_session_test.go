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

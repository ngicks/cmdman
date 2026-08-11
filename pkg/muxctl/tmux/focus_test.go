package tmux_test

import (
	"context"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/ngicks/cmdman/pkg/muxctl"
	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// TestFocusWindow_SelectsWithoutClient covers the half of focusing that needs
// nobody watching: the window becomes its session's current one, which is what
// a client attaching later will see. It is the landing path taken from outside
// the multiplexer, where switching is meaningless and attaching is the caller's
// job (see AttachCommand).
func TestFocusWindow_SelectsWithoutClient(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	srv := newServer(t, socket)
	sess, err := srv.New(ctx, muxctl.Config{SessionName: "work", WindowName: "cmdman-app"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := activeWindow(t, socket, "work"); got == sess.WindowID() {
		t.Fatalf("the new window is already active; the test would prove nothing")
	}

	if err := srv.FocusWindow(ctx, sess.WindowID(), muxctl.FocusOptions{}); err != nil {
		t.Fatalf("FocusWindow: %v", err)
	}
	if got := activeWindow(t, socket, "work"); got != sess.WindowID() {
		t.Errorf("active window = %s, want the focused %s", got, sess.WindowID())
	}
}

// TestFocusWindow_SwitchesClientAcrossSessions is D8's "no attached client on
// the target session": the caller sits in one session and the project's window
// lives in another, so landing has to move the caller's client — selecting the
// window alone would leave them looking at the same screen.
func TestFocusWindow_SwitchesClientAcrossSessions(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	srv := newServer(t, socket)
	// The caller's session, with a real attached client, and the project's
	// window built in a session of its own.
	if _, err := srv.New(ctx, muxctl.Config{
		SessionName: "here", WindowName: "cmdman-here",
	}); err != nil {
		t.Fatalf("New(here): %v", err)
	}
	sess, err := srv.New(ctx, muxctl.Config{
		SessionName: "there", WindowName: "cmdman-app",
	})
	if err != nil {
		t.Fatalf("New(there): %v", err)
	}
	attachClient(t, socket, "here")

	if err := srv.FocusWindow(ctx, sess.WindowID(), muxctl.FocusOptions{
		SwitchClient:  true,
		ClientSession: "here",
	}); err != nil {
		t.Fatalf("FocusWindow: %v", err)
	}

	waitForClientSession(t, socket, "there")
	if got := activeWindow(t, socket, "there"); got != sess.WindowID() {
		t.Errorf("active window in the landed-on session = %s, want %s", got, sess.WindowID())
	}
}

// TestFocusWindow_LeavesOtherClientsAlone is why the driver always passes -c:
// tmux resolves an omitted client from the caller's terminal, which a cmdman
// process summoned in a popup does not have — measured on a scratch server, a
// bare switch-client moved a *different* client. With no way to tell which
// client is the caller's, moving none is the only safe answer.
func TestFocusWindow_LeavesOtherClientsAlone(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	srv := newServer(t, socket)
	for _, name := range []string{"one", "two"} {
		if _, err := srv.New(ctx, muxctl.Config{
			SessionName: name, WindowName: "cmdman-" + name,
		}); err != nil {
			t.Fatalf("New(%s): %v", name, err)
		}
	}
	sess, err := srv.New(ctx, muxctl.Config{SessionName: "there", WindowName: "cmdman-app"})
	if err != nil {
		t.Fatalf("New(there): %v", err)
	}
	attachClient(t, socket, "one")
	attachClient(t, socket, "two")

	if err := srv.FocusWindow(ctx, sess.WindowID(), muxctl.FocusOptions{
		SwitchClient: true, // no ClientSession: the caller could not identify itself
	}); err != nil {
		t.Fatalf("FocusWindow: %v", err)
	}

	for _, got := range clientSessions(t, socket) {
		if got == "there" {
			t.Errorf("a client was moved to %q without knowing whose it was", got)
		}
	}
	// The window is still selected: that half never needed a client.
	if got := activeWindow(t, socket, "there"); got != sess.WindowID() {
		t.Errorf("active window = %s, want the focused %s", got, sess.WindowID())
	}
}

// TestAttachCommand_CarriesSocket pins the handoff argv: it must reach the same
// server everything else on this Server runs against, or the caller attaches to
// a tmux that knows nothing about the window just built.
func TestAttachCommand_CarriesSocket(t *testing.T) {
	srv, err := tmuxctl.Driver{}.Connect(
		context.Background(), muxctl.ServerConfig{Socket: "scratch-name"},
	)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	want := []string{"tmux", "-L", "scratch-name", "attach-session", "-t", "=work"}
	if got := srv.AttachCommand("work"); !slices.Equal(got, want) {
		t.Errorf("AttachCommand = %v, want %v", got, want)
	}

	pathSrv, err := tmuxctl.Driver{}.Connect(
		context.Background(),
		muxctl.ServerConfig{Executable: "/opt/tmux", Socket: "/run/cmdman.sock"},
	)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	want = []string{"/opt/tmux", "-S", "/run/cmdman.sock", "attach-session", "-t", "=work"}
	if got := pathSrv.AttachCommand("work"); !slices.Equal(got, want) {
		t.Errorf("AttachCommand = %v, want %v", got, want)
	}
}

// attachClient attaches a real tmux client to the session over a pty — the only
// way to exercise switch-client, which needs a client to move. The client is
// killed with the test.
func attachClient(t *testing.T, socket, session string) {
	t.Helper()
	cmd := exec.Command(requireTmux(t), "-L", socket, "attach-session", "-t", "="+session)
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("attach a client to %s: %v", session, err)
	}
	// tmux writes continuously to an attached client; an unread pty fills up and
	// stalls the server, so the output is drained for as long as the client lives.
	go func() { _, _ = io.Copy(io.Discard, ptmx) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
	})
	waitForClient(t, socket, session)
}

// activeWindow returns the id of the session's current window.
func activeWindow(t *testing.T, socket, session string) string {
	t.Helper()
	out := run(t, socket, "list-windows", "-t", "="+session,
		"-F", "#{window_id}\t#{window_active}")
	for line := range strings.SplitSeq(out, "\n") {
		if id, active, ok := strings.Cut(line, "\t"); ok && active == "1" {
			return id
		}
	}
	t.Fatalf("session %s has no active window; windows:\n%s", session, out)
	return ""
}

// clientSessions returns the session each attached client is looking at.
func clientSessions(t *testing.T, socket string) []string {
	t.Helper()
	out := run(t, socket, "list-clients", "-F", "#{client_session}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// waitForClient polls until a client is attached to the session: attaching goes
// through a pty and a fork, so it is not done when StartWithSize returns.
func waitForClient(t *testing.T, socket, session string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(clientSessions(t, socket), session) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no client attached to session %s", session)
}

// waitForClientSession polls until some client is looking at the session; the
// switch is a server-side action the test observes from the outside.
func waitForClientSession(t *testing.T, socket, session string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last []string
	for time.Now().Before(deadline) {
		last = clientSessions(t, socket)
		if slices.Contains(last, session) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no client switched to session %s (clients on %v)", session, last)
}

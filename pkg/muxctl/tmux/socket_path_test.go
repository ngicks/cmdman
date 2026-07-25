package tmux_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/muxctl"
	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// TestConnect_SocketPath round-trips the -S socket-path form (D7): a Socket
// value containing a path separator binds tmux to a socket file rather than a
// named server in the default dir, so a window built through that Server is
// enumerable on the same path and the socket file materializes where asked.
// Every other tmux test uses a bare -L name; this is the -S counterpart.
func TestConnect_SocketPath(t *testing.T) {
	requireTmux(t)
	sockPath := tempPath(t, "cmdman.sock")
	t.Cleanup(func() {
		_ = exec.Command(requireTmux(t), "-S", sockPath, "kill-server").Run()
	})

	const identity = "socket-path-id"
	srv, err := tmuxctl.Driver{}.Connect(
		context.Background(), muxctl.ServerConfig{Socket: sockPath},
	)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := srv.New(context.Background(), muxctl.Config{
		SessionName:   "cmdman-test",
		WindowName:    "cmdman",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The window is enumerable through the same -S socket path.
	rows, err := srv.ListWindows(context.Background(), muxctl.ListOptions{Identity: identity})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 owned window via -S socket, got %d: %v", len(rows), rows)
	}
	if rows[0].WindowID != sess.WindowID() {
		t.Errorf("WindowID = %q, want %q", rows[0].WindowID, sess.WindowID())
	}

	// The -S value is a socket file path, so tmux creates the file there.
	if !waitForFile(sockPath, time.Second) {
		t.Errorf("tmux -S socket file %q was not created", sockPath)
	}
}

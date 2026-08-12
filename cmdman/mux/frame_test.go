package mux

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// sleepLeaf is a viewer stand-in that stays alive, so "still running"
// assertions mean something.
func sleepLeaf(name string) muxctl.PaneSpec {
	return muxctl.PaneSpec{Leaf: muxctl.Leaf{
		Name: name,
		Cmd:  []string{"/bin/sh", "-c", "sleep 60"},
	}}
}

// framedWindow builds a cmdman-owned window holding a two-pane project layout
// with a one-entry frame around it, and returns the socket plus the window id.
// It drives the driver directly (test files are exempt from the mux →
// muxctl-only invariant) because the mux layer has no verb that shows a frame
// yet — the frame verbs are the parent plan's step, and this is what they will
// leave behind for List and Down to find.
func framedWindow(t *testing.T, identity, defName string) (socket, windowID string) {
	t.Helper()
	bin := tmuxOrSkip(t)
	socket = "cmdman-mux-" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _ = exec.Command(bin, "-L", socket, "kill-server").Run() })

	ctx := context.Background()
	server, err := tmux.Driver{}.Connect(ctx, muxctl.ServerConfig{Socket: socket})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := server.New(ctx, muxctl.Config{
		SessionName:      "framed",
		WindowName:       "dash",
		OwnedIdentity:    identity,
		ViewerDetachKeys: viewerDetachKeys,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	project := muxctl.PaneSpec{Container: muxctl.Container{
		Dir:    muxctl.DirHorizontal,
		Splits: []muxctl.Size{{N: 1}, {N: 1}},
		Panes:  []muxctl.PaneSpec{sleepLeaf("web"), sleepLeaf("worker")},
	}}
	if _, err := sess.ApplyLayout(ctx, project, 0); err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	// The shape frame.Spec.Carve produces: the entry leads, the main
	// placeholder stands for the panes that already exist.
	frameTree := muxctl.PaneSpec{Container: muxctl.Container{
		Dir:    muxctl.DirVertical,
		Splits: []muxctl.Size{{N: 3, Absolute: true}, {N: 1}},
		Panes: []muxctl.PaneSpec{
			sleepLeaf("frame-0"),
			{Leaf: muxctl.Leaf{Name: "main"}},
		},
	}}
	if err := sess.ShowFrame(ctx, frameTree, "main", defName); err != nil {
		t.Fatalf("ShowFrame: %v", err)
	}
	return socket, sess.WindowID()
}

// windowFormat expands a tmux format against the window, which yields "" for an
// option that was never set instead of failing.
func windowFormat(t *testing.T, bin, socket, windowID, format string) string {
	t.Helper()
	return tmuxRun(t, bin, socket, "display-message", "-p", "-t", windowID, format)
}

// framePaneCount counts the window's frame-stamped panes.
func framePaneCount(t *testing.T, bin, socket, windowID string) int {
	t.Helper()
	out := tmuxRun(
		t, bin, socket, "list-panes", "-t", windowID, "-F", "#{pane_id}\t#{@cmdman_frame}",
	)
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if _, stamp, _ := strings.Cut(line, "\t"); stamp != "" {
			n++
		}
	}
	return n
}

// TestList_ReportsTheFrameOnAFramedWindow pins what `mux ls` gains: the row for
// a framed window answers for its project exactly as before and carries the
// name of the frame around it, with no extra query per window.
func TestList_ReportsTheFrameOnAFramedWindow(t *testing.T) {
	const identity = "abc123-myproject"
	socket, windowID := framedWindow(t, identity, "dev")

	rows, err := List(context.Background(), ListOptions{
		Driver:   muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Identity: identity,
		Env:      []string{},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List returned %v, want the one framed window", rows)
	}
	if rows[0].WindowID != windowID {
		t.Errorf("WindowID = %q, want %q", rows[0].WindowID, windowID)
	}
	if rows[0].Identity != identity {
		t.Errorf("Identity = %q, want %q", rows[0].Identity, identity)
	}
	if rows[0].Frame != "dev" {
		t.Errorf("Frame = %q, want dev", rows[0].Frame)
	}
	if rows[0].Marker != 0 {
		t.Errorf("Marker = %d, want the applied 0: a frame pane must not break the vote",
			rows[0].Marker)
	}
}

// TestDown_OnAFramedWindowLeavesTheFrame is the mux-level reading of per-side
// teardown: `mux down` finds a framed window by the project identity it has
// always used, tears the project down, and leaves the frame standing and
// discoverable — chrome outlives the projects that arrive inside it.
func TestDown_OnAFramedWindowLeavesTheFrame(t *testing.T) {
	const identity = "abc123-myproject"
	socket, windowID := framedWindow(t, identity, "dev")
	bin := tmuxOrSkip(t)

	var out bytes.Buffer
	if err := Down(context.Background(), DownOptions{
		Driver:   muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Identity: identity,
		Env:      []string{},
		Stdout:   &out,
	}); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if !strings.Contains(out.String(), "Restored window") {
		t.Errorf("Down printed %q, want a restored-window line", out.String())
	}

	if got := framePaneCount(t, bin, socket, windowID); got != 1 {
		t.Errorf("frame panes after down = %d, want the frame's one still up", got)
	}
	if got := windowFormat(t, bin, socket, windowID, "#{@cmdman_frame_def}"); got != "dev" {
		t.Errorf("@cmdman_frame_def = %q after down, want dev", got)
	}
	if got := windowFormat(t, bin, socket, windowID, "#{@cmdman_window}"); got != "" {
		t.Errorf("@cmdman_window = %q after down, want it cleared", got)
	}

	// The window the project left behind is still cmdman's: the frame answers
	// for it, so the frame verbs can find it with no project to name it by.
	rows, err := List(context.Background(), ListOptions{
		Driver: muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Env:    []string{},
	})
	if err != nil {
		t.Fatalf("List after down: %v", err)
	}
	if len(rows) != 1 || rows[0].WindowID != windowID {
		t.Fatalf("List after down returned %v, want the framed window %s", rows, windowID)
	}
	if rows[0].Identity != "" || rows[0].Frame != "dev" {
		t.Errorf("row after down = {Identity:%q Frame:%q}, want {\"\" \"dev\"}",
			rows[0].Identity, rows[0].Frame)
	}

	// And the project's own query no longer matches it.
	mine, err := List(context.Background(), ListOptions{
		Driver:   muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Identity: identity,
		Env:      []string{},
	})
	if err != nil {
		t.Fatalf("List by identity after down: %v", err)
	}
	if len(mine) != 0 {
		t.Errorf("identity %q still matches %v after down", identity, mine)
	}
}

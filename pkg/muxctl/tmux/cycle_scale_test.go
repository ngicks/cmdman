package tmux_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/muxctl"
	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// paneOption returns the pane-scoped value of a tmux user option, tolerating
// errors by returning "" — an unset option is exactly what some assertions check for.
func paneOption(t *testing.T, socket, paneID, name string) string {
	t.Helper()
	out, err := exec.Command(
		requireTmux(t), "-L", socket,
		"show-options", "-p", "-t", paneID, "-v", name,
	).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestApplyLayout_StampsAndClearsLeafOption tests that ApplyLayout stamps
// @cmdman_leaf on panes with CycleKey set and clears it on panes without.
func TestApplyLayout_StampsAndClearsLeafOption(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	// Layout with two leaves: one has CycleKey ("web"), one does not.
	root := muxctl.PaneSpec{
		Container: muxctl.Container{
			Dir:    muxctl.DirHorizontal,
			Splits: []muxctl.Size{{N: 1}, {N: 1}},
			Panes: []muxctl.PaneSpec{
				{
					Leaf: muxctl.Leaf{
						Name:     "web",
						Cmd:      []string{"/bin/sh", "-c", "sleep 60"},
						CycleKey: "web",
					},
				},
				{
					Leaf: muxctl.Leaf{
						Name: "worker",
						Cmd:  []string{"/bin/sh", "-c", "sleep 60"},
						// no CycleKey
					},
				},
			},
		},
	}

	panes, err := sess.ApplyLayout(context.Background(), root, 0)
	if err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}

	webID := panes["web"].PaneId()
	workerID := panes["worker"].PaneId()

	// web pane must carry @cmdman_leaf = "web".
	if got := paneOption(t, socket, webID, "@cmdman_leaf"); got != "web" {
		t.Errorf("web pane @cmdman_leaf = %q, want %q", got, "web")
	}
	// worker pane must NOT carry @cmdman_leaf.
	if got := paneOption(t, socket, workerID, "@cmdman_leaf"); got != "" {
		t.Errorf("worker pane @cmdman_leaf = %q, want empty", got)
	}

	// Re-apply with roles swapped: worker now has CycleKey, web does not.
	root2 := muxctl.PaneSpec{
		Container: muxctl.Container{
			Dir:    muxctl.DirHorizontal,
			Splits: []muxctl.Size{{N: 1}, {N: 1}},
			Panes: []muxctl.PaneSpec{
				{
					Leaf: muxctl.Leaf{
						Name: "web",
						Cmd:  []string{"/bin/sh", "-c", "sleep 60"},
						// no CycleKey now
					},
				},
				{
					Leaf: muxctl.Leaf{
						Name:     "worker",
						Cmd:      []string{"/bin/sh", "-c", "sleep 60"},
						CycleKey: "worker",
					},
				},
			},
		},
	}

	panes2, err := sess.ApplyLayout(context.Background(), root2, 1)
	if err != nil {
		t.Fatalf("ApplyLayout (second): %v", err)
	}

	webID2 := panes2["web"].PaneId()
	workerID2 := panes2["worker"].PaneId()

	if got := paneOption(t, socket, webID2, "@cmdman_leaf"); got != "" {
		t.Errorf("web pane @cmdman_leaf after swap = %q, want empty", got)
	}
	if got := paneOption(t, socket, workerID2, "@cmdman_leaf"); got != "worker" {
		t.Errorf("worker pane @cmdman_leaf after swap = %q, want %q", got, "worker")
	}
}

// TestScaleState_ReadWriteMerge tests ReadScalePositions, WriteScalePosition
// merge behavior, and removal via pos=0.
func TestScaleState_ReadWriteMerge(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// Create a minimal session/window to write options onto.
	run(t, socket, "new-session", "-d", "-s", "scale-test", "-n", "dash")
	wid := run(t, socket, "list-windows", "-t", "scale-test", "-F", "#{window_id}")

	opts := tmuxctl.ListOwnedWindowsOptions{Socket: socket}

	// Initially empty.
	pos, err := tmuxctl.ReadScalePositions(context.Background(), opts, wid)
	if err != nil {
		t.Fatalf("ReadScalePositions (empty): %v", err)
	}
	if pos != nil {
		t.Errorf("initial ReadScalePositions = %v, want nil", pos)
	}

	// Write web=2.
	if err := tmuxctl.WriteScalePosition(context.Background(), opts, wid, "web", 2); err != nil {
		t.Fatalf("WriteScalePosition web=2: %v", err)
	}

	pos, err = tmuxctl.ReadScalePositions(context.Background(), opts, wid)
	if err != nil {
		t.Fatalf("ReadScalePositions after web=2: %v", err)
	}
	if pos["web"] != 2 {
		t.Errorf("pos[web] = %d, want 2", pos["web"])
	}

	// Write worker=1 (merge, not replace).
	if err := tmuxctl.WriteScalePosition(context.Background(), opts, wid, "worker", 1); err != nil {
		t.Fatalf("WriteScalePosition worker=1: %v", err)
	}

	pos, err = tmuxctl.ReadScalePositions(context.Background(), opts, wid)
	if err != nil {
		t.Fatalf("ReadScalePositions after worker=1: %v", err)
	}
	if pos["web"] != 2 {
		t.Errorf("after merge pos[web] = %d, want 2", pos["web"])
	}
	if pos["worker"] != 1 {
		t.Errorf("after merge pos[worker] = %d, want 1", pos["worker"])
	}

	// Remove worker (write pos=0 removes the key).
	if err := tmuxctl.WriteScalePosition(context.Background(), opts, wid, "worker", 0); err != nil {
		t.Fatalf("WriteScalePosition worker=0: %v", err)
	}

	pos, err = tmuxctl.ReadScalePositions(context.Background(), opts, wid)
	if err != nil {
		t.Fatalf("ReadScalePositions after worker removal: %v", err)
	}
	if _, ok := pos["worker"]; ok {
		t.Errorf("worker still present after removal: %v", pos)
	}
	if pos["web"] != 2 {
		t.Errorf("web changed after worker removal: got %d, want 2", pos["web"])
	}
}

// TestDetach_ClearsScaleOption verifies that Detach unsets @cmdman_scale so the
// restored window does not carry stale cycle-scale state into a fresh session.
func TestDetach_ClearsScaleOption(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "detach-scale-test"
	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		SessionName:   "detach-test",
		WindowName:    "cmdman",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	opts := tmuxctl.ListOwnedWindowsOptions{Socket: socket}

	// Write a scale position so the option exists.
	if err := tmuxctl.WriteScalePosition(
		context.Background(), opts, sess.WindowID(), "web", 3,
	); err != nil {
		t.Fatalf("WriteScalePosition: %v", err)
	}

	// Pre-condition: option is set.
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_scale"); got == "" {
		t.Fatal("precondition: @cmdman_scale should be set before Detach")
	}

	if err := sess.Detach(context.Background()); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	// Post-condition: scale option cleared.
	if got := windowOption(t, socket, sess.WindowID(), "@cmdman_scale"); got != "" {
		t.Errorf("@cmdman_scale = %q after Detach, want empty", got)
	}
}

// TestFindLeafPane_AndRespawnLeaf tests that FindLeafPane locates a pane by
// cycle key and that RespawnLeaf replaces the pane's process while preserving
// the @cmdman_leaf stamp.
func TestFindLeafPane_AndRespawnLeaf(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")

	ready := tempPath(t, "ready")
	replaced := tempPath(t, "replaced")

	// First process writes "ready" sentinel.
	script1 := ": >" + ready + "; sleep 60"
	root := muxctl.PaneSpec{
		Leaf: muxctl.Leaf{
			Name:     "web",
			Cmd:      []string{"/bin/sh", "-c", script1},
			CycleKey: "web",
		},
	}
	panes, err := sess.ApplyLayout(context.Background(), root, 0)
	if err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}
	webID := panes["web"].PaneId()

	// Wait for first process to signal readiness.
	if !waitForFile(ready, 3*time.Second) {
		t.Fatal("first process never became ready")
	}

	// FindLeafPane must locate the pane by cycle key.
	opts := tmuxctl.ListOwnedWindowsOptions{Socket: socket}
	foundID, ok, err := tmuxctl.FindLeafPane(
		context.Background(), opts, sess.WindowID(), "web",
	)
	if err != nil {
		t.Fatalf("FindLeafPane: %v", err)
	}
	if !ok {
		t.Fatal("FindLeafPane: expected to find pane with @cmdman_leaf=web")
	}
	if foundID != webID {
		t.Errorf("FindLeafPane returned %q, want %q", foundID, webID)
	}

	// RespawnLeaf replaces the pane with a new process (writes "replaced").
	script2 := ": >" + replaced + "; sleep 60"
	newLeaf := muxctl.Leaf{
		Name:     "web",
		Cmd:      []string{"/bin/sh", "-c", script2},
		CycleKey: "web",
	}
	if err := tmuxctl.RespawnLeaf(context.Background(), sess, foundID, newLeaf); err != nil {
		t.Fatalf("RespawnLeaf: %v", err)
	}

	// Wait for replacement process sentinel.
	if !waitForFile(replaced, 3*time.Second) {
		t.Fatal("replacement process never wrote sentinel")
	}

	// @cmdman_leaf should still be "web" after RespawnLeaf.
	if got := paneOption(t, socket, foundID, "@cmdman_leaf"); got != "web" {
		t.Errorf("@cmdman_leaf after RespawnLeaf = %q, want web", got)
	}
}

// TestListOwnedWindows_ReturnsScalePositions verifies that ListOwnedWindows
// includes ScalePositions decoded from the @cmdman_scale window option.
func TestListOwnedWindows_ReturnsScalePositions(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "scale-list-test"
	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:        socket,
		SessionName:   "scale-sess",
		WindowName:    "dash",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	opts := tmuxctl.ListOwnedWindowsOptions{Socket: socket}

	// Initially no scale positions.
	rows, err := tmuxctl.ListOwnedWindows(context.Background(), tmuxctl.ListOwnedWindowsOptions{
		Socket:   socket,
		Identity: identity,
	})
	if err != nil {
		t.Fatalf("ListOwnedWindows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].ScalePositions != nil {
		t.Errorf("initial ScalePositions = %v, want nil", rows[0].ScalePositions)
	}

	// Write web=2.
	if err := tmuxctl.WriteScalePosition(
		context.Background(), opts, sess.WindowID(), "web", 2,
	); err != nil {
		t.Fatalf("WriteScalePosition: %v", err)
	}

	rows, err = tmuxctl.ListOwnedWindows(context.Background(), tmuxctl.ListOwnedWindowsOptions{
		Socket:   socket,
		Identity: identity,
	})
	if err != nil {
		t.Fatalf("ListOwnedWindows after write: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].ScalePositions["web"] != 2 {
		t.Errorf("ScalePositions[web] = %d, want 2", rows[0].ScalePositions["web"])
	}
}

package tmux_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/muxctl"
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

// TestScaleState_ReadWrite tests the driver's window-state KV surface for the
// "scale" key: WriteWindowState stores the space-joined "name=pos" wire format
// verbatim, ReadWindowState hands it back, and an empty write unsets the option
// (decoding and read-modify-write of that string are a pkg/cmdman/mux concern,
// not the driver's).
func TestScaleState_ReadWrite(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	// Create a minimal session/window to write options onto.
	run(t, socket, "new-session", "-d", "-s", "scale-test", "-n", "dash")
	wid := run(t, socket, "list-windows", "-t", "scale-test", "-F", "#{window_id}")

	srv := newServer(t, socket)

	// Initially unset → empty string, no error.
	raw, err := srv.ReadWindowState(context.Background(), wid, "scale")
	if err != nil {
		t.Fatalf("ReadWindowState (empty): %v", err)
	}
	if raw != "" {
		t.Errorf("initial ReadWindowState = %q, want empty", raw)
	}

	// Write "web=2".
	if err := srv.WriteWindowState(
		context.Background(),
		wid,
		"scale",
		"web=2",
	); err != nil {
		t.Fatalf("WriteWindowState web=2: %v", err)
	}
	raw, err = srv.ReadWindowState(context.Background(), wid, "scale")
	if err != nil {
		t.Fatalf("ReadWindowState after web=2: %v", err)
	}
	if raw != "web=2" {
		t.Errorf("ReadWindowState = %q, want %q", raw, "web=2")
	}

	// Overwrite with a two-command encoding.
	if err := srv.WriteWindowState(
		context.Background(), wid, "scale", "web=2 worker=1",
	); err != nil {
		t.Fatalf("WriteWindowState web=2 worker=1: %v", err)
	}
	raw, err = srv.ReadWindowState(context.Background(), wid, "scale")
	if err != nil {
		t.Fatalf("ReadWindowState after two-command write: %v", err)
	}
	if raw != "web=2 worker=1" {
		t.Errorf("ReadWindowState = %q, want %q", raw, "web=2 worker=1")
	}

	// Empty write unsets the option entirely.
	if err := srv.WriteWindowState(context.Background(), wid, "scale", ""); err != nil {
		t.Fatalf("WriteWindowState (empty): %v", err)
	}
	raw, err = srv.ReadWindowState(context.Background(), wid, "scale")
	if err != nil {
		t.Fatalf("ReadWindowState after empty write: %v", err)
	}
	if raw != "" {
		t.Errorf("ReadWindowState after unset = %q, want empty", raw)
	}
}

// TestDetach_ClearsScaleOption verifies that Detach unsets @cmdman_scale so the
// restored window does not carry stale cycle-scale state into a fresh session.
func TestDetach_ClearsScaleOption(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "detach-scale-test"
	srv := newServer(t, socket)
	sess, err := srv.New(context.Background(), muxctl.Config{
		SessionName:   "detach-test",
		WindowName:    "cmdman",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Write a scale position so the option exists.
	if err := srv.WriteWindowState(
		context.Background(), sess.WindowID(), "scale", "web=3",
	); err != nil {
		t.Fatalf("WriteWindowState: %v", err)
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

// TestFindPane_AndRespawnLeaf tests that FindPane locates a pane by
// cycle key and that RespawnLeaf replaces the pane's process while preserving
// the @cmdman_leaf stamp.
func TestFindPane_AndRespawnLeaf(t *testing.T) {
	requireTmux(t)
	sess, socket := newSession(t, "cmdman")
	srv := newServer(t, socket)

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

	// FindPane must locate the pane by cycle key.
	foundID, ok, err := srv.FindPane(
		context.Background(), sess.WindowID(), "web",
	)
	if err != nil {
		t.Fatalf("FindPane: %v", err)
	}
	if !ok {
		t.Fatal("FindPane: expected to find pane with @cmdman_leaf=web")
	}
	if foundID != webID {
		t.Errorf("FindPane returned %q, want %q", foundID, webID)
	}

	// RespawnLeaf replaces the pane with a new process (writes "replaced").
	script2 := ": >" + replaced + "; sleep 60"
	newLeaf := muxctl.Leaf{
		Name:     "web",
		Cmd:      []string{"/bin/sh", "-c", script2},
		CycleKey: "web",
	}
	if err := sess.RespawnLeaf(context.Background(), foundID, newLeaf); err != nil {
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

// TestListWindows_ReturnsScaleState verifies that ListWindows reports the raw
// @cmdman_scale window option verbatim in Window.State when StateKeyScale is
// requested via ListOptions.StateKeys (decoding is a caller concern).
func TestListWindows_ReturnsScaleState(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identity = "scale-list-test"
	srv := newServer(t, socket)
	sess, err := srv.New(context.Background(), muxctl.Config{
		SessionName:   "scale-sess",
		WindowName:    "dash",
		OwnedIdentity: identity,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Initially no scale positions.
	rows, err := srv.ListWindows(context.Background(), muxctl.ListOptions{
		Identity:  identity,
		StateKeys: []muxctl.StateKey{muxctl.StateKeyScale},
	})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].State["scale"] != "" {
		t.Errorf("initial State[scale] = %q, want empty", rows[0].State["scale"])
	}

	// Write "web=2".
	if err := srv.WriteWindowState(
		context.Background(), sess.WindowID(), "scale", "web=2",
	); err != nil {
		t.Fatalf("WriteWindowState: %v", err)
	}

	rows, err = srv.ListWindows(context.Background(), muxctl.ListOptions{
		Identity:  identity,
		StateKeys: []muxctl.StateKey{muxctl.StateKeyScale},
	})
	if err != nil {
		t.Fatalf("ListWindows after write: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].State["scale"] != "web=2" {
		t.Errorf("State[scale] = %q, want %q", rows[0].State["scale"], "web=2")
	}
}

// TestListWindows_MultipleStateKeys exercises the inline-state fetch with
// more than one key at once and covers the boundary cases the single-key test
// does not: a key set on one window but absent on another (with an interior
// empty field preserved), and a requested key that is unset everywhere (absent
// → "" per the contract).
func TestListWindows_MultipleStateKeys(t *testing.T) {
	requireTmux(t)
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })

	const identA = "multi-state-a"
	const identB = "multi-state-b"

	srv := newServer(t, socket)
	sessA, err := srv.New(context.Background(), muxctl.Config{
		SessionName:   "session-a",
		WindowName:    "dash-a",
		OwnedIdentity: identA,
	})
	if err != nil {
		t.Fatalf("New session-a: %v", err)
	}
	sessB, err := srv.New(context.Background(), muxctl.Config{
		SessionName:   "session-b",
		WindowName:    "dash-b",
		OwnedIdentity: identB,
	})
	if err != nil {
		t.Fatalf("New session-b: %v", err)
	}

	// Window A: both "scale" and "layout" set.
	if err := srv.WriteWindowState(
		context.Background(), sessA.WindowID(), "scale", "web=2",
	); err != nil {
		t.Fatalf("WriteWindowState A scale: %v", err)
	}
	if err := srv.WriteWindowState(
		context.Background(), sessA.WindowID(), "layout", "alpha",
	); err != nil {
		t.Fatalf("WriteWindowState A layout: %v", err)
	}
	// Window B: only "layout" set — "scale" stays absent, so its interior field
	// in the -F output is empty and must be preserved (not shifted).
	if err := srv.WriteWindowState(
		context.Background(), sessB.WindowID(), "layout", "beta",
	); err != nil {
		t.Fatalf("WriteWindowState B layout: %v", err)
	}

	// Request three keys at once; "baz" is unset on every window.
	rows, err := srv.ListWindows(context.Background(), muxctl.ListOptions{
		StateKeys: []muxctl.StateKey{muxctl.StateKeyScale, "layout", "baz"},
	})
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}

	byIdentity := make(map[string]muxctl.Window)
	for _, row := range rows {
		byIdentity[row.Identity] = row
	}

	rowA, ok := byIdentity[identA]
	if !ok {
		t.Fatalf("identity %q not found; got %v", identA, rows)
	}
	if rowA.State["scale"] != "web=2" {
		t.Errorf("A.State[scale] = %q, want web=2", rowA.State["scale"])
	}
	if rowA.State["layout"] != "alpha" {
		t.Errorf("A.State[layout] = %q, want alpha", rowA.State["layout"])
	}
	if rowA.State["baz"] != "" {
		t.Errorf("A.State[baz] = %q, want empty (unset key)", rowA.State["baz"])
	}

	rowB, ok := byIdentity[identB]
	if !ok {
		t.Fatalf("identity %q not found; got %v", identB, rows)
	}
	if rowB.State["scale"] != "" {
		t.Errorf("B.State[scale] = %q, want empty (absent on this window)", rowB.State["scale"])
	}
	if rowB.State["layout"] != "beta" {
		t.Errorf("B.State[layout] = %q, want beta", rowB.State["layout"])
	}
	if rowB.State["baz"] != "" {
		t.Errorf("B.State[baz] = %q, want empty (unset key)", rowB.State["baz"])
	}
}

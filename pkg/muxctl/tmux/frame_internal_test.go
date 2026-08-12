package tmux

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// frameTestSession builds a Session against a scratch tmux server on a
// per-test socket and hands back the executor the unexported helpers take.
// The frame stamp is written and read below the exported surface, so these
// cases live in the driver's own package rather than beside the black-box
// suites in helpers_test.go.
func frameTestSession(t *testing.T) (*Session, *executor) {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("tmux not in PATH: %v", err)
	}
	socket := "muxctl-" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _ = exec.Command(tmuxPath, "-L", socket, "kill-server").Run() })

	srv, err := Driver{}.Connect(context.Background(), muxctl.ServerConfig{Socket: socket})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	sess, err := srv.New(context.Background(), muxctl.Config{
		SessionName: "cmdman-test",
		WindowName:  "cmdman",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sess.(*Session), srv.(*Server).exec
}

// splitPane splits a 3-row pane off the top of the window and returns its id.
func splitPane(t *testing.T, e *executor, windowID string) string {
	t.Helper()
	id, err := e.run(
		context.Background(), "split-window", "-v", "-b", "-d", "-l", "3",
		"-t", windowID, "-P", "-F", "#{pane_id}",
	)
	if err != nil {
		t.Fatalf("split-window on %s: %v", windowID, err)
	}
	return id
}

// paneOption reads a per-pane user option through a format expansion, which
// yields "" for an option that was never set instead of failing.
func paneOption(t *testing.T, e *executor, paneID, name string) string {
	t.Helper()
	out, err := e.run(
		context.Background(), "display-message", "-p", "-t", paneID, "#{"+name+"}",
	)
	if err != nil {
		t.Fatalf("display-message %s for %s: %v", name, paneID, err)
	}
	return out
}

// carvedTree mirrors what frame.Spec.Carve produces for [top 3c, left 20c]
// around a main placeholder: nested two-child containers, each dividing what its
// predecessor left over.
func carvedTree() muxctl.PaneSpec {
	return muxctl.PaneSpec{Container: muxctl.Container{
		Dir:    muxctl.DirVertical,
		Splits: []muxctl.Size{{N: 3, Absolute: true}, {N: 1}},
		Panes: []muxctl.PaneSpec{
			{Leaf: muxctl.Leaf{Name: "frame-0", Cmd: []string{"statusbar"}}},
			{Container: muxctl.Container{
				Dir:    muxctl.DirHorizontal,
				Splits: []muxctl.Size{{N: 20, Absolute: true}, {N: 1}},
				Panes: []muxctl.PaneSpec{
					{Leaf: muxctl.Leaf{Name: "frame-1", Cmd: []string{"switcher"}}},
					{Leaf: muxctl.Leaf{Name: "main"}},
				},
			}},
		},
	}}
}

func TestPlanFrame_SizesEachEntryAgainstTheRemainder(t *testing.T) {
	plan, ok := planFrame(carvedTree(), "main", 80, 24)
	if !ok {
		t.Fatal("main placeholder not found in the carved tree")
	}
	// Outside-in: the top entry divides the window, the left entry divides only
	// the rectangle below it.
	want := []framePlacement{
		{
			node:   muxctl.PaneSpec{Leaf: muxctl.Leaf{Name: "frame-0"}},
			dir:    muxctl.DirVertical,
			before: true, cells: 3, w: 80, h: 3,
		},
		{
			node:   muxctl.PaneSpec{Leaf: muxctl.Leaf{Name: "frame-1"}},
			dir:    muxctl.DirHorizontal,
			before: true, cells: 20, w: 20, h: 20,
		},
	}
	if len(plan) != len(want) {
		t.Fatalf("plan = %+v, want one placement per entry", plan)
	}
	for i, w := range want {
		got := plan[i]
		if got.node.Name != w.node.Name {
			t.Errorf("plan[%d] node = %q, want %q", i, got.node.Name, w.node.Name)
		}
		if got.dir != w.dir || got.before != w.before {
			t.Errorf("plan[%d] split = %q before=%v, want %q before=%v",
				i, got.dir, got.before, w.dir, w.before)
		}
		if got.cells != w.cells || got.w != w.w || got.h != w.h {
			t.Errorf("plan[%d] = %d cells (%dx%d), want %d cells (%dx%d)",
				i, got.cells, got.w, got.h, w.cells, w.w, w.h)
		}
	}
}

// TestPlanFrame_PercentResolvesAgainstTheRemainder pins the arithmetic D30
// names: a percentage entry is a percentage of the rectangle its predecessors
// left over, not of the window. The 12-cell entry ahead of it leaves 11 rows of
// a 24-row window (one goes to the separator), so 50% is 5 — not the 12 the
// same def would mean read against the window.
func TestPlanFrame_PercentResolvesAgainstTheRemainder(t *testing.T) {
	// The shape frame.Spec.Carve produces for [top 12c, bottom 50%]: the
	// percentage entry trails the main region, so it divides only what the
	// absolute entry above it left.
	root := muxctl.PaneSpec{Container: muxctl.Container{
		Dir:    muxctl.DirVertical,
		Splits: []muxctl.Size{{N: 12, Absolute: true}, {N: 1}},
		Panes: []muxctl.PaneSpec{
			{Leaf: muxctl.Leaf{Name: "frame-0", Cmd: []string{"statusbar"}}},
			{Container: muxctl.Container{
				Dir:    muxctl.DirVertical,
				Splits: []muxctl.Size{{N: 1}, {N: 50, Percent: true}},
				Panes: []muxctl.PaneSpec{
					{Leaf: muxctl.Leaf{Name: "main"}},
					{Leaf: muxctl.Leaf{Name: "frame-1", Cmd: []string{"log"}}},
				},
			}},
		},
	}}

	plan, ok := planFrame(root, "main", 80, 24)
	if !ok || len(plan) != 2 {
		t.Fatalf("plan = %+v (ok=%v), want one placement per entry", plan, ok)
	}
	if plan[0].cells != 12 {
		t.Errorf("absolute entry got %d cells, want 12", plan[0].cells)
	}
	if plan[1].before {
		t.Error("percent entry leads the main region; a bottom entry trails it")
	}
	if plan[1].cells != 5 {
		t.Errorf(
			"percent entry got %d cells, want 5 (50%% of the 11 rows left over), not 12",
			plan[1].cells,
		)
	}
}

func TestPlanFrame_RejectsATreeWithoutTheMainPane(t *testing.T) {
	if plan, ok := planFrame(carvedTree(), "elsewhere", 80, 24); ok {
		t.Errorf("planFrame reported ok for a tree with no main pane: %+v", plan)
	}
}

// TestPlanFrame_UncoveredChildIsTooSmallToRealize pins the malformed-tree
// tolerance: a container whose splits do not cover its panes is what
// [muxctl.MuxSpec.Validate] would reject, but a tree carrying the main
// placeholder cannot be validated, so the planner must not index past its cells.
func TestPlanFrame_UncoveredChildIsTooSmallToRealize(t *testing.T) {
	root := muxctl.PaneSpec{Container: muxctl.Container{
		Dir:    muxctl.DirVertical,
		Splits: []muxctl.Size{{N: 1}},
		Panes: []muxctl.PaneSpec{
			{Leaf: muxctl.Leaf{Name: "main"}},
			{Leaf: muxctl.Leaf{Name: "frame-0", Cmd: []string{"statusbar"}}},
		},
	}}
	plan, ok := planFrame(root, "main", 80, 24)
	if !ok || len(plan) != 1 {
		t.Fatalf("plan = %+v (ok=%v), want the one uncovered entry", plan, ok)
	}
	if plan[0].cells != 0 {
		t.Errorf("uncovered entry got %d cells, want 0 so ShowFrame skips it", plan[0].cells)
	}
}

func TestStampLeaf_FrameStampReadBackAsFramePane(t *testing.T) {
	ctx := context.Background()
	s, e := frameTestSession(t)

	frameID := splitPane(t, e, s.windowID)

	// A marker and a cycle key are passed in deliberately: keeping frame panes
	// out of both polls is the write path's job, not the caller's.
	leaf := muxctl.PaneSpec{Leaf: muxctl.Leaf{
		Name:     "frame-0",
		Cmd:      []string{"/bin/sh", "-c", "sleep 60"},
		CycleKey: "widget",
	}}
	if err := s.stampLeaf(ctx, frameID, leaf, false, 4, true); err != nil {
		t.Fatalf("stampLeaf: %v", err)
	}

	panes, err := listPanesByRole(ctx, e, s.windowID)
	if err != nil {
		t.Fatalf("listPanesByRole: %v", err)
	}
	if !slices.Equal(panes.frame, []string{frameID}) {
		t.Errorf("frame panes = %v, want [%s]", panes.frame, frameID)
	}
	if slices.Contains(panes.project, frameID) {
		t.Errorf("frame pane %s also listed as a project pane: %v", frameID, panes.project)
	}
	if len(panes.project) != 1 {
		t.Errorf("project panes = %v, want only the window's original pane", panes.project)
	}
	for _, opt := range []string{markerOption, leafOption} {
		if got := paneOption(t, e, frameID, opt); got != "" {
			t.Errorf("%s on frame pane = %q, want unset", opt, got)
		}
	}
}

func TestStampLeaf_ProjectStampClearsStaleFrameMark(t *testing.T) {
	ctx := context.Background()
	s, e := frameTestSession(t)

	paneID := splitPane(t, e, s.windowID)
	if _, err := e.run(
		ctx, "set-option", "-p", "-t", paneID, frameOption, frameStamp,
	); err != nil {
		t.Fatalf("set %s: %v", frameOption, err)
	}

	leaf := muxctl.PaneSpec{Leaf: muxctl.Leaf{
		Name: "api",
		Cmd:  []string{"/bin/sh", "-c", "sleep 60"},
	}}
	if err := s.stampLeaf(ctx, paneID, leaf, false, 2, false); err != nil {
		t.Fatalf("stampLeaf: %v", err)
	}

	panes, err := listPanesByRole(ctx, e, s.windowID)
	if err != nil {
		t.Fatalf("listPanesByRole: %v", err)
	}
	if len(panes.frame) != 0 {
		t.Errorf("frame panes = %v, want none after a project stamp", panes.frame)
	}
	if !slices.Contains(panes.project, paneID) {
		t.Errorf("project panes = %v, want %s among them", panes.project, paneID)
	}
	if got := paneOption(t, e, paneID, markerOption); got != "2" {
		t.Errorf("%s = %q, want 2", markerOption, got)
	}
}

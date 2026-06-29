package tmux_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// fakeTmux writes an executable stub that stands in for the tmux binary so a
// test can drive ApplyLayout without a real tmux server and inspect the exact
// order of subcommands it issues (Config.Path routes the executor at it).
//
// The stub appends each invocation's verb (its first argument) to a record file
// and emulates just enough of tmux for ApplyLayout to complete: list-panes
// yields a single anchor pane, display-message yields a fixed pane size, and
// split-window hands back a fresh pane id from a counter. The returned func
// reads back the recorded verbs in invocation order.
func fakeTmux(t *testing.T) (path string, recorded func() []string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "tmux")
	rec := `"` + filepath.Join(dir, "record.log") + `"`
	cnt := `"` + filepath.Join(dir, "counter") + `"`
	script := "#!/bin/sh\n" +
		"verb=\"$1\"\n" +
		"echo \"$verb\" >> " + rec + "\n" +
		"case \"$verb\" in\n" +
		"list-panes) echo '%0' ;;\n" +
		"display-message) printf '200\\t50\\n' ;;\n" +
		"split-window)\n" +
		"  n=$(cat " + cnt + " 2>/dev/null || echo 0)\n" +
		"  n=$((n + 1))\n" +
		"  echo \"$n\" > " + cnt + "\n" +
		"  echo \"%$n\" ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	return path, func() []string {
		b, err := os.ReadFile(strings.Trim(rec, `"`))
		if err != nil {
			t.Fatalf("read fake tmux record: %v", err)
		}
		out := strings.TrimSpace(string(b))
		if out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}
}

// TestApplyLayout_RespawnsAfterAllSplits pins the fix for the stale-size bug:
// ApplyLayout must finish building the window geometry (all split-window calls)
// before it respawns any viewer (respawn-pane). A viewer respawned mid-build
// boots at an intermediate pane size and loses the corrective SIGWINCH from a
// later split, so full-screen apps render too small until a manual border drag.
//
// Three sibling leaves force multiple split-window calls, so the old
// interleaved build (respawn each leaf right after its split) and the new
// two-pass build (all splits, then all respawns) produce a visibly different
// command order; the assertion below fails on the old order.
func TestApplyLayout_RespawnsAfterAllSplits(t *testing.T) {
	path, recorded := fakeTmux(t)

	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Path:     path,
		WindowID: "@1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	root := muxctl.PaneSpec{
		Container: muxctl.Container{
			Dir:    muxctl.DirHorizontal,
			Splits: []muxctl.Size{{N: 1}, {N: 1}, {N: 1}},
			Panes: []muxctl.PaneSpec{
				{Leaf: muxctl.Leaf{Name: "a", Cmd: []string{"sh"}}},
				{Leaf: muxctl.Leaf{Name: "b", Cmd: []string{"sh"}}},
				{Leaf: muxctl.Leaf{Name: "c", Cmd: []string{"sh"}}},
			},
		},
	}

	panes, err := sess.ApplyLayout(context.Background(), root, 5)
	if err != nil {
		t.Fatalf("ApplyLayout: %v", err)
	}

	// Behavior preserved: every leaf name is present in the returned map.
	if got := sortedKeys(panes); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("panes = %v, want [a b c]", got)
	}

	cmds := recorded()
	nSplit, nRespawn := 0, 0
	lastSplit, firstRespawn := -1, -1
	for i, c := range cmds {
		switch c {
		case "split-window":
			nSplit++
			lastSplit = i
		case "respawn-pane":
			nRespawn++
			if firstRespawn == -1 {
				firstRespawn = i
			}
		}
	}
	// Sanity: the layout must actually exercise multiple splits and one respawn
	// per leaf, otherwise the order assertion would be vacuous.
	if nSplit < 2 {
		t.Fatalf(
			"want >= 2 split-window calls to exercise interleaving, got %d; cmds = %v",
			nSplit,
			cmds,
		)
	}
	if nRespawn != 3 {
		t.Errorf("want 3 respawn-pane calls (one per leaf), got %d; cmds = %v", nRespawn, cmds)
	}

	// The invariant that fixes the bug: no respawn-pane is interleaved before
	// the geometry is fully built — every respawn occurs after the last split.
	if firstRespawn <= lastSplit {
		t.Errorf(
			"respawn-pane interleaved before geometry: first respawn %d, last split %d; cmds = %v",
			firstRespawn,
			lastSplit,
			cmds,
		)
	}
}

package mux

import (
	"context"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// identityTestSession names the session the identity tests bring their
// dashboards up in.
const identityTestSession = "identity"

// identitySocket starts a per-test tmux server name and registers its teardown.
// Unlike [projectWindow] nothing is built on it: these tests want [Run] itself
// to be what creates every window.
func identitySocket(t *testing.T) (bin, socket string) {
	t.Helper()
	bin = tmuxOrSkip(t)
	socket = "cmdman-mux-" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { _ = exec.Command(bin, "-L", socket, "kill-server").Run() })
	return bin, socket
}

// runUp brings the two-pane dashboard of [upSpec] up for identity, under the
// window name a project of that name would wear. The env is empty on purpose:
// outside tmux there is no current window to take over, so the window under test
// is the one Run resolved by identity or built for itself.
func runUp(t *testing.T, socket, windowName, identity string) {
	t.Helper()
	err := Run(context.Background(), upSpec(socket), RunOptions{
		SessionName: identityTestSession,
		WindowName:  windowName,
		Identity:    identity,
		Env:         []string{},
		Stdout:      io.Discard,
	})
	if err != nil {
		t.Fatalf("Run (identity %q): %v", identity, err)
	}
}

// theWindowOf returns the single window carrying identity, failing when the
// project owns none or more than one.
func theWindowOf(t *testing.T, socket, identity string) OwnedWindow {
	t.Helper()
	rows, err := List(context.Background(), ListOptions{
		Driver:   muxctl.DriverSpec{Name: "tmux", Socket: socket},
		Identity: identity,
		Env:      []string{},
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("windows carrying identity %q = %v, want exactly one", identity, rows)
	}
	return rows[0]
}

// TestRun_ReusesTheRenamedWindowOfItsProject is the false-negative half of
// identity-keyed lookup: a window is the project's because it carries the
// project's stamp, not because it still answers to the name it was built under.
// Anything renames a window — the user, an in-pane program's title escape, tmux
// automatic-rename — and a second `mux up` that went by the name would have
// built a second dashboard beside the first.
func TestRun_ReusesTheRenamedWindowOfItsProject(t *testing.T) {
	const identity = "wdhash-web"
	bin, socket := identitySocket(t)

	runUp(t, socket, "cmdman-web", identity)
	built := theWindowOf(t, socket, identity)

	tmuxRun(t, bin, socket, "rename-window", "-t", built.WindowID, "vim")

	runUp(t, socket, "cmdman-web", identity)

	again := theWindowOf(t, socket, identity)
	if again.WindowID != built.WindowID {
		t.Errorf("second up landed on %s, want the project's own %s",
			again.WindowID, built.WindowID)
	}
	if again.WindowName != "vim" {
		t.Errorf("window name = %q, want the user's vim: the name is theirs to set",
			again.WindowName)
	}
}

// TestRun_SameWindowNameIsNotTheSameProject is the false-positive half, and the
// bug that prompted identity-keyed lookup: one repository checked out twice, or
// any two projects sharing a name, brought up from different work directories.
// Their windows wear the same label, so a lookup by name hands the second up the
// first's window — rebuilding its panes and restamping it, which orphans the
// first project from its own down, land and cycle.
func TestRun_SameWindowNameIsNotTheSameProject(t *testing.T) {
	const (
		identityA = "wdhash-a-web"
		identityB = "wdhash-b-web"
	)
	bin, socket := identitySocket(t)

	runUp(t, socket, "cmdman-web", identityA)
	theirs := theWindowOf(t, socket, identityA)
	before := windowPaneIDs(t, bin, socket, theirs.WindowID)

	runUp(t, socket, "cmdman-web", identityB)
	mine := theWindowOf(t, socket, identityB)

	if mine.WindowID == theirs.WindowID {
		t.Fatalf("both projects landed in %s; a shared window name is not a shared project",
			theirs.WindowID)
	}
	if mine.WindowName != theirs.WindowName {
		t.Errorf("window names = %q and %q, want both to wear cmdman-web",
			theirs.WindowName, mine.WindowName)
	}

	// The first project keeps its window whole: same stamp, so its own down /
	// land / cycle still find it, and same panes, so the viewers it was showing
	// were never rebuilt.
	if got := windowFormat(t, bin, socket, theirs.WindowID, "#{@cmdman_window}"); got != identityA {
		t.Errorf("their ownership stamp = %q, want %q", got, identityA)
	}
	if got := windowPaneIDs(t, bin, socket, theirs.WindowID); !slices.Equal(got, before) {
		t.Errorf("their panes = %v, want the untouched %v", got, before)
	}
	if got := theWindowOf(t, socket, identityA); got.WindowID != theirs.WindowID {
		t.Errorf("identity %q now finds %s, want %s", identityA, got.WindowID, theirs.WindowID)
	}
}

// windowPaneIDs returns the ids of every pane in windowID, in tmux list order.
func windowPaneIDs(t *testing.T, bin, socket, windowID string) []string {
	t.Helper()
	out := tmuxRun(t, bin, socket, "list-panes", "-t", windowID, "-F", "#{pane_id}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

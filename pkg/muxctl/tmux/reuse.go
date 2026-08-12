package tmux

import (
	"context"
	"strconv"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// currentWindow is the caller's current tmux window as the takeover rules read
// it: both identity slots, plus the name and pane count the unowned-reuse rule
// consults.
type currentWindow struct {
	id       string
	name     string
	panes    int
	identity string // ownerOption — which project holds the window
	frameDef string // frameDefOption — which frame is shown around it
}

// readCurrentWindow resolves the window the caller is sitting in. ok is false
// when there is no such window (no server, no attached client) or the output
// does not parse — both mean "decide without a current window", never an error.
func readCurrentWindow(ctx context.Context, e *executor) (currentWindow, bool) {
	out, err := e.run(
		ctx, "display-message", "-p",
		"#{window_id}\t#{window_name}\t#{window_panes}"+
			"\t#{"+ownerOption+"}\t#{"+frameDefOption+"}",
	)
	if err != nil {
		return currentWindow{}, false
	}
	// executor.run trims trailing whitespace and tmux drops trailing empty
	// fields, so the same window yields three field counts depending on which
	// slots are stamped: 3 for neither, 4 for the owner alone, 5 for both. Each
	// optional field is therefore read only once its presence is established —
	// indexing blind would read the owner slot as the frame slot.
	parts := strings.SplitN(out, "\t", 5)
	if len(parts) < 3 {
		return currentWindow{}, false
	}
	panes, err := strconv.Atoi(parts[2])
	if err != nil {
		return currentWindow{}, false
	}
	cur := currentWindow{id: parts[0], name: parts[1], panes: panes}
	if len(parts) > 3 {
		cur.identity = parts[3]
	}
	if len(parts) > 4 {
		cur.frameDef = parts[4]
	}
	return cur, true
}

// currentWindowToReuse decides whether the caller's current tmux window should
// be taken over in place rather than building a separate named window. It
// returns the current window's id and ok=true when that window is safe to
// reuse; ok=false means the caller should fall back to find-or-create by name.
//
// A window is reused when it carries ownedIdentity in the @cmdman_window slot
// (we built it for this same caller on a previous run, so cycling stays in
// place regardless of how many panes the user has since added), when it holds a
// frame but no project, when it is already named like the owned window, or when
// it has a single pane (an "empty" window safe to repurpose).
//
// The identity must MATCH: a window stamped for another project is left alone,
// because taking it over rebuilds its region out from under it and leaves the
// other project's stamp on a window that no longer answers for it. A caller
// with no identity of its own owns nothing, so no stamped window is its own to
// reuse either.
//
// A window holding a frame and no project is the show-before-launch case — the
// frame was put up as chrome waiting for a project — so the project lands in
// the main region the frame leaves over instead of opening elsewhere. It is an
// explicit branch because such a window is neither single-pane nor named like
// ours: its frame panes are exactly the ones the unowned rule reads as
// unrelated work.
//
// The old all-panes-marked check is intentionally NOT used here: it breaks as
// soon as the user manually splits a pane into the dashboard window, which is a
// common workflow.
func currentWindowToReuse(
	ctx context.Context,
	e *executor,
	ownedWindowName, ownedIdentity string,
) (string, bool) {
	cur, ok := readCurrentWindow(ctx, e)
	if !ok {
		return "", false
	}
	switch {
	case cur.identity != "":
		if ownedIdentity == "" || cur.identity != ownedIdentity {
			return "", false
		}
	case cur.frameDef == "":
		if !muxctl.ShouldReuseUnmarkedWindow(cur.name, ownedWindowName, cur.panes) {
			return "", false
		}
	}
	return cur.id, true
}

// currentWindowIfOwned returns the caller's current window id when that window
// carries ownedIdentity in the @cmdman_window ownership option — i.e. a
// previous [New] call stamped it for this same caller. Unlike
// [currentWindowToReuse] it accepts nothing else: teardown callers (e.g.
// [Session.Detach] via [Open]) must act only on a window that is provably
// theirs, never on an unowned window the user happens to be sitting in, and
// never on another project's dashboard.
//
// A window carrying a frame but no project is deliberately not accepted here
// either: opening it hands a teardown caller a window it never claimed. The
// frame side's own teardown addresses its window through the frame slot, which
// [Server.ListWindows] reports — no current-window guess involved.
//
// This replaces the older currentWindowIfMarked (every-pane-marked check),
// which broke whenever the user manually added a pane to the dashboard window.
func currentWindowIfOwned(ctx context.Context, e *executor, ownedIdentity string) (string, bool) {
	if ownedIdentity == "" {
		return "", false
	}
	cur, ok := readCurrentWindow(ctx, e)
	if !ok || cur.id == "" {
		return "", false
	}
	return cur.id, cur.identity == ownedIdentity
}

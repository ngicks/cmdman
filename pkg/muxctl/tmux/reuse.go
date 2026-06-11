package tmux

import (
	"context"
	"strconv"
	"strings"
)

// currentWindowToReuse decides whether the caller's current tmux window should
// be taken over in place rather than building a separate named window. It
// returns the current window's id and ok=true when that window is safe to
// reuse; ok=false means the caller should fall back to find-or-create by name.
//
// A window is reused when it already carries the @cmdman_window ownership
// option (we built it on a previous run, so cycling stays in place regardless
// of how many panes the user has since added), when it is already named like
// the owned window, or when it has a single pane (an "empty" window safe to
// repurpose). The old all-panes-marked check is intentionally NOT used here:
// it breaks as soon as the user manually splits a pane into the dashboard
// window, which is a common workflow.
func currentWindowToReuse(
	ctx context.Context,
	e *executor,
	ownedWindowName string,
) (string, bool) {
	out, err := e.run(
		ctx, "display-message", "-p",
		"#{window_id}\t#{window_name}\t#{window_panes}\t#{"+ownerOption+"}",
	)
	if err != nil {
		return "", false
	}
	// executor.run trims trailing whitespace from tmux's output; when
	// @cmdman_window is unset the trailing tab is stripped, yielding 3 fields
	// instead of 4. Accept either length: a missing 4th field means an empty
	// (unset) identity.
	parts := strings.SplitN(out, "\t", 4)
	if len(parts) < 3 {
		return "", false
	}
	id, name := parts[0], parts[1]
	identity := ""
	if len(parts) == 4 {
		identity = parts[3]
	}
	panes, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", false
	}
	// A window we stamped before carries a non-empty @cmdman_window; recognise
	// it regardless of its name or pane count so a re-run cycles in place.
	if identity != "" {
		return id, true
	}
	if shouldReuseUnmarkedWindow(name, ownedWindowName, panes) {
		return id, true
	}
	return "", false
}

// shouldReuseUnmarkedWindow decides whether an unowned current window should
// be taken over: when it is already named like ours or has at most a single
// pane (so repurposing it does not clobber unrelated work).
func shouldReuseUnmarkedWindow(curName, ownedName string, panes int) bool {
	return curName == ownedName || panes <= 1
}

// currentWindowIfOwned returns the caller's current window id when that window
// carries the @cmdman_window ownership option — i.e. it was stamped by a
// previous [New] call. Unlike [currentWindowToReuse] it never accepts an
// unowned window: teardown callers (e.g. [Session.Detach] via [OpenExisting])
// must act only on a provably muxctl-owned window, never repurpose an arbitrary
// window the user happens to be sitting in.
//
// This replaces the older currentWindowIfMarked (every-pane-marked check),
// which broke whenever the user manually added a pane to the dashboard window.
func currentWindowIfOwned(ctx context.Context, e *executor) (string, bool) {
	out, err := e.run(
		ctx, "display-message", "-p",
		"#{window_id}\t#{"+ownerOption+"}",
	)
	if err != nil {
		return "", false
	}
	id, identity, ok := strings.Cut(strings.TrimSpace(out), "\t")
	if !ok || id == "" || identity == "" {
		return "", false
	}
	return id, true
}

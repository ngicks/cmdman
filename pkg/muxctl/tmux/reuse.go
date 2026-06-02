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
// A window is reused when it already carries our per-pane marker (we built it
// on a previous run, so cycling stays in place), when it is already named like
// the owned window, or when it has a single pane (an "empty" window safe to
// repurpose). This mirrors the resolution the muxctltester performs.
func currentWindowToReuse(
	ctx context.Context,
	e *executor,
	ownedWindowName string,
) (string, bool) {
	out, err := e.run(
		ctx, "display-message", "-p",
		"#{window_id}\t#{window_name}\t#{window_panes}",
	)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(out, "\t", 3)
	if len(parts) != 3 {
		return "", false
	}
	id, name := parts[0], parts[1]
	panes, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", false
	}
	// A window we built before carries the marker on every pane; recognize it
	// regardless of its name or pane count so a re-run cycles in place.
	if windowIsMarked(ctx, e, id) {
		return id, true
	}
	if shouldReuseUnmarkedWindow(name, ownedWindowName, panes) {
		return id, true
	}
	return "", false
}

// shouldReuseUnmarkedWindow decides whether an unmarked current window should
// be taken over: when it is already named like ours or has at most a single
// pane (so repurposing it does not clobber unrelated work).
func shouldReuseUnmarkedWindow(curName, ownedName string, panes int) bool {
	return curName == ownedName || panes <= 1
}

// currentWindowIfMarked returns the caller's current window id when that window
// carries the muxctl marker on every pane (i.e. it is a dashboard a previous
// ApplyLayout built). Unlike currentWindowToReuse it never accepts an unmarked
// window: teardown callers (e.g. [Session.Detach] via [OpenExisting]) must act
// only on a provably muxctl-owned window, not repurpose an arbitrary one.
func currentWindowIfMarked(ctx context.Context, e *executor) (string, bool) {
	out, err := e.run(ctx, "display-message", "-p", "#{window_id}")
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", false
	}
	if windowIsMarked(ctx, e, id) {
		return id, true
	}
	return "", false
}

// windowIsMarked reports whether every pane of windowID carries a numeric
// @cmdman_marker option — i.e. the window was built by a previous ApplyLayout.
func windowIsMarked(ctx context.Context, e *executor, windowID string) bool {
	out, err := e.run(ctx, "list-panes", "-t", windowID, "-F", "#{"+markerOption+"}")
	if err != nil || out == "" {
		return false
	}
	for line := range strings.SplitSeq(out, "\n") {
		if _, err := strconv.Atoi(line); err != nil {
			return false
		}
	}
	return true
}

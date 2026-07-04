package muxctl

// ShouldReuseUnmarkedWindow decides whether an unowned current window should
// be taken over: when it is already named like ours or has at most a single
// pane (so repurposing it does not clobber unrelated work).
func ShouldReuseUnmarkedWindow(curName, ownedName string, panes int) bool {
	return curName == ownedName || panes <= 1
}

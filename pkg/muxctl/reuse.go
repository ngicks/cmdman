package muxctl

// ShouldReuseUnmarkedWindow decides whether an unowned current window should
// be taken over: when it is already named like ours or has at most a single
// pane (so repurposing it does not clobber unrelated work).
//
// It answers for a window carrying no cmdman state at all. A window that holds
// an identity — a project's or a frame's — is decided by the driver on that
// identity before this rule is reached, since its panes are cmdman's own and
// the pane count says nothing about whose work they are.
func ShouldReuseUnmarkedWindow(curName, ownedName string, panes int) bool {
	return curName == ownedName || panes <= 1
}

package tui

import (
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// commandMatches reports whether a command row matches the filter on command
// name or status/display label.
func commandMatches(filter string, c core.CommandRow) bool {
	if core.MatchesFilter(filter, c.Name) {
		return true
	}
	if core.MatchesFilter(filter, string(c.State)) {
		return true
	}
	if core.MatchesFilter(filter, core.DisplayLabel(c.State, c.ExitCode)) {
		return true
	}
	return false
}

// composeRowMatches reports whether a compose row matches the filter on project
// name, path, or visible metadata.
func composeRowMatches(filter string, r composeRow) bool {
	if core.MatchesFilter(filter, r.name) {
		return true
	}
	if core.MatchesFilter(filter, r.path) {
		return true
	}
	if core.MatchesFilter(filter, r.workdir) {
		return true
	}
	if core.MatchesFilter(filter, r.modified) {
		return true
	}
	return false
}

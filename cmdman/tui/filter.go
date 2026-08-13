package tui

import (
	"strings"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// matchesFilter reports whether needle matches haystack. Matching is a simple
// case-insensitive subsequence (fuzzy) match: every rune of needle must appear
// in haystack in order. An empty needle always matches.
func matchesFilter(needle, haystack string) bool {
	if needle == "" {
		return true
	}
	needle = strings.ToLower(needle)
	haystack = strings.ToLower(haystack)
	// Fast path: contiguous substring.
	if strings.Contains(haystack, needle) {
		return true
	}
	ni := 0
	nr := []rune(needle)
	for _, hc := range haystack {
		if hc == nr[ni] {
			ni++
			if ni == len(nr) {
				return true
			}
		}
	}
	return false
}

// commandMatches reports whether a command row matches the filter on command
// name or status/display label.
func commandMatches(filter string, c core.CommandRow) bool {
	if matchesFilter(filter, c.Name) {
		return true
	}
	if matchesFilter(filter, string(c.State)) {
		return true
	}
	if matchesFilter(filter, core.DisplayLabel(c.State, c.ExitCode)) {
		return true
	}
	return false
}

// composeRowMatches reports whether a compose row matches the filter on project
// name, path, or visible metadata.
func composeRowMatches(filter string, r composeRow) bool {
	if matchesFilter(filter, r.name) {
		return true
	}
	if matchesFilter(filter, r.path) {
		return true
	}
	if matchesFilter(filter, r.workdir) {
		return true
	}
	if matchesFilter(filter, r.modified) {
		return true
	}
	return false
}

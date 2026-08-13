package core

import "strings"

// MatchesFilter reports whether needle matches haystack. Matching is a simple
// case-insensitive subsequence (fuzzy) match: every rune of needle must appear
// in haystack in order. An empty needle always matches.
func MatchesFilter(needle, haystack string) bool {
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

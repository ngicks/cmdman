package tmux

import (
	"iter"
	"slices"
	"strings"
)

// interpolateKeys returns a new slice with placeholders replaced in each key string.
// It never mutates the input slice.
func interpolateKeys(keys []string, sessionID, windowID, paneID string) iter.Seq[string] {
	if len(keys) == 0 {
		return slices.Values(keys)
	}
	return func(yield func(string) bool) {
		for _, k := range keys {
			if !yield(interpolateKey(k, sessionID, windowID, paneID)) {
				return
			}
		}
	}
}

var interpolationTarget = [...]string{
	"SESSION_ID",
	"WINDOW_ID",
	"PANE_ID",
	"INJECT_META",
}

// interpolateKey replaces placeholders in a single key string.
//
// Supported placeholders:
//   - #{SESSION_ID}  — replaced with the tmux session ID (e.g. $0)
//   - #{WINDOW_ID}   — replaced with the tmux window ID (e.g. @1)
//   - #{PANE_ID}     — replaced with the tmux pane ID (e.g. %3)
//   - #{INJECT_META} — replaced with an export command setting CRAB_SESSION_ID,
//     CRAB_WINDOW_ID and CRAB_PANE_ID
//
// Escaping: ##{...} produces the literal #{...}.
func interpolateKey(key, sessionID, windowID, paneID string) string {
	// Quick check: if there's no '#' at all, nothing to do.
	if !strings.Contains(key, "#{") {
		return key
	}

	replacements := map[string]string{
		"SESSION_ID": sessionID,
		"WINDOW_ID":  windowID,
		"PANE_ID":    paneID,
		"INJECT_META": "export CRAB_SESSION_ID='" + sessionID +
			"' CRAB_WINDOW_ID='" + windowID +
			"' CRAB_PANE_ID='" + paneID + "'",
	}

	var b strings.Builder
	b.Grow(len(key))

	i := 0
LOOP:
	for i < len(key) {
		// Look for '#' characters.
		if key[i] != '#' {
			j := i + 1
			for ; j < len(key) && key[j] != '#'; j++ {
			}
			b.WriteString(key[i:j])
			i = j
			continue
		}

		for _, k := range interpolationTarget {
			if strings.HasPrefix(key[i:], "##{"+k+"}") {
				b.WriteString("#{" + k + "}")
				i += len("##{" + k + "}")
				continue LOOP
			}
			if strings.HasPrefix(key[i:], "#{"+k+"}") {
				b.WriteString(replacements[k])
				i += len("#{" + k + "}")
				continue LOOP
			}
		}

		j := i + 1
		for ; j < len(key) && key[j] != '#'; j++ {
		}
		b.WriteString(key[i:j])
		i = j
	}

	return b.String()
}

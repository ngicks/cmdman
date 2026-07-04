package mux

import (
	"slices"
	"strconv"
	"strings"
)

// The mux layer stores cycle-scale positions under [muxctl.StateKeyScale] via
// the muxctl driver's window-state KV; the driver maps it onto its own native
// per-window storage (tmux: the @cmdman_scale option). The space-joined
// "name=pos" wire format that slot holds is decoded/encoded by this file (the
// mux layer owns the scale codec), keeping "scale" semantics out of muxctl.

// decodeScalePositions parses a space-joined "name=pos" string as stored in the
// driver's scale option (tmux's @cmdman_scale). Malformed pairs (missing "=",
// non-numeric pos, empty name, pos <= 0) are silently skipped — hostile or
// empty option strings must decode gracefully. Compose command names match
// [A-Za-z0-9._-] so space-splitting is unambiguous.
func decodeScalePositions(raw string) map[string]int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	m := make(map[string]int)
	for pair := range strings.FieldsSeq(raw) {
		name, posStr, ok := strings.Cut(pair, "=")
		if !ok || name == "" || posStr == "" {
			continue
		}
		pos, err := strconv.Atoi(posStr)
		if err != nil || pos <= 0 {
			continue
		}
		m[name] = pos
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// encodeScalePositions serializes a map[string]int to the space-joined
// "name=pos" wire format. Keys are emitted in sorted order for determinism.
// Entries with pos <= 0 are skipped. Returns "" when the map is empty or all
// entries are skipped.
func encodeScalePositions(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var sb strings.Builder
	first := true
	for _, k := range keys {
		v := m[k]
		if v <= 0 {
			continue
		}
		if !first {
			sb.WriteByte(' ')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(strconv.Itoa(v))
		first = false
	}
	return sb.String()
}

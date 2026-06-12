package tmux

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// scaleOption is the window-level tmux user option that stores the
// per-command replica positions for cycle-scale, encoded as
// space-joined "name=pos" pairs (e.g. "web=2 worker=1").
//
// Window-level: survives pane churn, layout cycling, and ApplyLayout's window
// reset. Cleared by Session.Detach alongside ownerOption so a fresh dashboard
// starts every command at replica 1.
//
// NOTE: window-level user options are a tmux-specific feature with no direct
// equivalent in zellij or wezterm — same portability caveat as markerOption
// and leafOption.
const scaleOption = "@cmdman_scale"

// decodeScalePositions parses a space-joined "name=pos" string as stored in
// scaleOption. Malformed pairs (missing "=", non-numeric pos, empty name, pos
// <= 0) are silently skipped — hostile or empty option strings must decode
// gracefully. Compose command names match [A-Za-z0-9._-] so space-splitting
// is unambiguous.
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

// ReadScalePositions reads the @cmdman_scale option from windowID and decodes
// it into a map[string]int (command name → 1-based replica position). A
// missing or empty option returns a nil map (no error). Malformed pairs are
// silently skipped.
func ReadScalePositions(
	ctx context.Context,
	opts ListOwnedWindowsOptions,
	windowID string,
) (map[string]int, error) {
	e := newExecutor(opts.Path, opts.Socket)
	out, err := e.run(
		ctx, "show-options", "-w", "-t", windowID, "-v", scaleOption,
	)
	if err != nil {
		// show-options exits non-zero when the option is absent; treat as empty.
		return nil, nil
	}
	return decodeScalePositions(out), nil
}

// WriteScalePosition performs a read-modify-write of the @cmdman_scale option
// on windowID: it reads the current map, sets cmd to pos, and writes the
// updated encoding back. pos must be > 0; pos <= 0 removes cmd from the map.
func WriteScalePosition(
	ctx context.Context,
	opts ListOwnedWindowsOptions,
	windowID, cmd string,
	pos int,
) error {
	e := newExecutor(opts.Path, opts.Socket)
	// Read current value.
	raw, err := e.run(
		ctx, "show-options", "-w", "-t", windowID, "-v", scaleOption,
	)
	if err != nil {
		// Missing option is fine — start from an empty map.
		raw = ""
	}
	m := decodeScalePositions(raw)
	if m == nil {
		m = make(map[string]int)
	}
	if pos <= 0 {
		delete(m, cmd)
	} else {
		m[cmd] = pos
	}
	encoded := encodeScalePositions(m)
	if encoded == "" {
		// Nothing left: unset the option entirely.
		_, _ = e.run(ctx, "set-option", "-w", "-u", "-t", windowID, scaleOption)
		return nil
	}
	if _, err := e.run(
		ctx, "set-option", "-w", "-t", windowID, scaleOption, encoded,
	); err != nil {
		return fmt.Errorf("tmux: write scale positions for window %s: %w", windowID, err)
	}
	return nil
}

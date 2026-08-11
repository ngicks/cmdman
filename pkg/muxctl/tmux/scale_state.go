package tmux

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// userOptionPrefix is the tmux user-option namespace cmdman stores per-window
// state under. A state key "scale" maps to the window-level option
// "@cmdman_scale"; see [stateOption].
const userOptionPrefix = "@cmdman_"

// stateOption maps an opaque per-window state key to the tmux window-level user
// option that backs it. muxctl passes opaque [muxctl.StateKey] tokens (e.g.
// "scale"); the driver owns this key → option-name mapping so that vocabulary
// stays out of muxctl.
func stateOption(key muxctl.StateKey) string {
	return userOptionPrefix + string(key)
}

// scaleOption is the window-level tmux user option that stores the
// per-command replica positions for cycle-scale (state key "scale"). It equals
// stateOption("scale"); it is named here so [Session.Detach] can clear it
// alongside the other cmdman window state.
//
// Window-level: survives pane churn, layout cycling, and ApplyLayout's window
// reset. Cleared by Session.Detach alongside ownerOption so a fresh dashboard
// starts every command at replica 1.
//
// NOTE: window-level user options are a tmux-specific feature with no direct
// equivalent in zellij or wezterm — same portability caveat as markerOption
// and leafOption.
const scaleOption = userOptionPrefix + "scale"

// ReadWindowState reads the opaque per-window state value stored under key on
// windowID. The tmux driver backs each key with the window-level user option
// stateOption(key) (e.g. key "scale" → @cmdman_scale). A missing or empty
// option returns "" (no error). muxctl does not interpret the value; decoding
// it is the caller's responsibility (see cmdman/mux, which owns the scale
// codec), keeping "scale" semantics out of the driver.
func (srv *Server) ReadWindowState(
	ctx context.Context,
	windowID string,
	key muxctl.StateKey,
) (string, error) {
	e := srv.exec
	out, err := e.run(
		ctx, "show-options", "-w", "-t", windowID, "-v", stateOption(key),
	)
	if err != nil {
		// show-options exits non-zero when the option is absent; treat as empty.
		return "", nil
	}
	return out, nil
}

// WriteWindowState stores value as the window-level user option
// stateOption(key) on windowID. An empty value unsets the option entirely, so
// a fully-cleared state leaves no stale value behind. muxctl treats the value
// as opaque; the caller owns any encoding (the scale read-modify-write over the
// decoded map lives in cmdman/mux, which owns the scale codec).
func (srv *Server) WriteWindowState(
	ctx context.Context,
	windowID string,
	key muxctl.StateKey,
	value string,
) error {
	e := srv.exec
	if value == "" {
		// Nothing to store: unset the option entirely.
		_, _ = e.run(ctx, "set-option", "-w", "-u", "-t", windowID, stateOption(key))
		return nil
	}
	if _, err := e.run(
		ctx, "set-option", "-w", "-t", windowID, stateOption(key), value,
	); err != nil {
		return fmt.Errorf(
			"tmux: write window state %s for window %s: %w", stateOption(key), windowID, err,
		)
	}
	return nil
}

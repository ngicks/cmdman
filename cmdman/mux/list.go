package mux

import (
	"context"
	"fmt"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// OwnedWindow is a mux-layer row returned by [List]: one multiplexer window
// carrying cmdman state — a project it holds, a frame shown around it, or both.
// The fields mirror [muxctl.Window] but are defined here so future non-tmux
// drivers fit without exposing a driver-private type to the rest of the stack.
type OwnedWindow struct {
	// SessionName is the multiplexer session the window belongs to.
	SessionName string
	// WindowID is the driver-assigned window identifier (e.g. tmux "@3").
	WindowID string
	// WindowName is the human-visible window name, for display and diagnostics
	// only: it is neither unique nor stable, so which project a window holds is
	// the Identity's answer alone. A takeover window keeps its original name,
	// two projects named alike wear the same one, and a rename changes it under
	// everybody.
	WindowName string
	// Identity is the opaque string the caller supplied as
	// [RunOptions.Identity] (or its default) when [Run] built this dashboard.
	// For compose mux this is <wdhash>-<escaped-project>; for standalone mux
	// it defaults to the resolved window name.
	Identity string
	// Frame is the name of the frame def currently shown around this window,
	// or "" when it is unframed. A window can carry a frame without holding a
	// project (a frame shown before anything was launched, or a project torn
	// down under a frame that stayed), which is why it is listed alongside
	// Identity rather than derived from it.
	Frame string
	// Marker is the layout index last applied to this window (from
	// [muxctl.Session.StatWindow]), or -1 when no layout has been applied or
	// the panes carry inconsistent markers.
	Marker int
	// ScalePositions holds the per-command cycle-scale positions decoded from the
	// window's @cmdman_scale option (command name → 1-based replica position).
	// Absent commands default to position 1 at consumption time.
	// Nil when the option is unset or empty.
	ScalePositions map[string]int
}

// ListOptions configures [List].
type ListOptions struct {
	// Driver selects and configures the multiplexer server. An empty Driver.Name
	// autodetects from Env the same way [Run] does ($TMUX > $ZELLIJ > tmux);
	// Path/Socket/Opts feed [muxctl.Driver.Connect] and must match those used
	// when [Run] built the dashboard.
	Driver muxctl.DriverSpec
	// SessionName, when non-empty, restricts the listing to that session only.
	// Empty returns every window carrying cmdman state on the server.
	SessionName string
	// Identity, when non-empty, filters the results to windows whose ownership
	// stamp equals this string exactly — so a framed window answers for its
	// project as an unframed one does, and a window carrying only a frame
	// answers for no project at all. Empty returns every window carrying
	// cmdman state, whichever side holds it.
	Identity string
	// Env is the process env consulted for driver autodetection. Empty defaults
	// to os.Environ().
	Env []string
}

// List returns the windows carrying cmdman state on the target multiplexer
// server. It is a thin layer over [muxctl.Server.ListWindows]: it resolves
// the server via [resolveServer] (mapping the known-but-unimplemented
// "zellij"/"wezterm" to a "not implemented yet" error and any other
// unregistered driver name to a lookup error, exactly as [Run] does), maps the
// caller options to driver options, and re-exports the rows as mux-level
// [OwnedWindow] values so upper layers (cmdman/cli presentation,
// cmd/cmdman/commands) never import a driver-private type.
//
// No printing is performed here — presentation is [cmdman/cli]'s job
// (workstream 3).
func List(ctx context.Context, opts ListOptions) ([]OwnedWindow, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}

	server, err := resolveServer(ctx, opts.Driver, env)
	if err != nil {
		return nil, err
	}

	rows, err := server.ListWindows(ctx, muxctl.ListOptions{
		Session:   opts.SessionName,
		Identity:  opts.Identity,
		StateKeys: []muxctl.StateKey{muxctl.StateKeyScale},
	})
	if err != nil {
		return nil, fmt.Errorf("mux: enumerate owned windows: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	out := make([]OwnedWindow, len(rows))
	for i, r := range rows {
		out[i] = OwnedWindow{
			SessionName:    r.SessionName,
			WindowID:       r.WindowID,
			WindowName:     r.WindowName,
			Identity:       r.Identity,
			Frame:          r.Frame,
			Marker:         r.Marker,
			ScalePositions: decodeScalePositions(r.State[muxctl.StateKeyScale]),
		}
	}
	return out, nil
}

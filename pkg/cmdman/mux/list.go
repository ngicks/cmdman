package mux

import (
	"context"
	"fmt"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// OwnedWindow is a mux-layer row returned by [List]: it describes a single
// cmdman-owned multiplexer window. The fields mirror [tmux.OwnedWindow] but
// are defined here so future non-tmux drivers fit without exposing a
// driver-private type to the rest of the stack.
type OwnedWindow struct {
	// SessionName is the multiplexer session the window belongs to.
	SessionName string
	// WindowID is the driver-assigned window identifier (e.g. tmux "@3").
	WindowID string
	// WindowName is the human-visible window name. It may differ from the
	// Identity — a takeover window keeps its original name while the identity
	// stamp records the cmdman-assigned ownership value.
	WindowName string
	// Identity is the opaque string the caller supplied as
	// [RunOptions.Identity] (or its default) when [Run] built this dashboard.
	// For compose mux this is <wdhash>-<escaped-project>; for standalone mux
	// it defaults to the resolved window name.
	Identity string
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
	// Driver selects the multiplexer driver. Empty autodetects from Env the
	// same way [Run] does ($TMUX > $ZELLIJ > tmux).
	Driver string
	// DriverOpt carries driver-specific options; the tmux driver honors "path"
	// and "socket". Must match the options used when [Run] built the dashboard.
	DriverOpt map[string]string
	// SessionName, when non-empty, restricts the listing to that session only.
	// Empty returns all stamped windows on the server.
	SessionName string
	// Identity, when non-empty, filters the results to windows whose ownership
	// stamp equals this string exactly. Empty returns every stamped window.
	Identity string
	// Env is the process env consulted for driver autodetection. Empty defaults
	// to os.Environ().
	Env []string
}

// List returns the cmdman-owned windows visible on the target multiplexer
// server. It is a thin layer over [tmux.ListOwnedWindows]: it resolves the
// driver (returning a "not implemented" error for non-tmux drivers, as [Run]
// does), maps the caller options to driver options, and re-exports the rows as
// mux-level [OwnedWindow] values so upper layers (pkg/cmdman/cli presentation,
// cmd/cmdman/commands) never import a driver-private type.
//
// No printing is performed here — presentation is [pkg/cmdman/cli]'s job
// (workstream 3).
func List(ctx context.Context, opts ListOptions) ([]OwnedWindow, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}

	driver := resolveDriver(opts.Driver, env)
	if driver != "tmux" {
		return nil, fmt.Errorf(
			"mux: driver %q is not implemented yet (v1 ships tmux only)", driver,
		)
	}

	rows, err := tmux.ListOwnedWindows(ctx, tmux.ListOwnedWindowsOptions{
		Path:     opts.DriverOpt["path"],
		Socket:   opts.DriverOpt["socket"],
		Session:  opts.SessionName,
		Identity: opts.Identity,
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
			Marker:         r.Marker,
			ScalePositions: r.ScalePositions,
		}
	}
	return out, nil
}

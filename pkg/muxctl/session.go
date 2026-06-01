package muxctl

import "context"

// Session controls the cmdman-owned window in one multiplexer session.
//
// A single command invocation owns exactly one window. The named entries in
// [MuxSpec.Layouts] are switchable configurations the user picks among via
// repeated calls to ApplyLayout — they are NOT separate windows. This is what
// distinguishes a cmdman mux session from a general multi-window dashboard.
//
// Session reuse, socket choice, dedicated-server isolation, and the choice
// of window name belong to each driver's constructor (e.g. pkg/muxctl/tmux.New),
// not to this interface.
//
// Implementations issue commands to the underlying multiplexer; they MUST NOT
// host a tty/pty themselves. The supplied [context.Context] cancels in-flight
// CLI commands.
type Session interface {
	// ApplyLayout (re)builds the controlled window's pane tree to match root.
	// It RESETS the window's panes — switching among MuxSpec.Layouts is done
	// by passing each layout's Root in turn. Returns the resulting runtime
	// panes keyed by pane name (PaneSpec.Name).
	//
	// marker is an opaque non-negative integer the driver records on each
	// pane in driver-specific state (the tmux driver uses a per-pane user
	// option); muxctl does not interpret it. Pass marker < 0 to skip
	// recording it. Consumers (the cmdman mux family, the muxctltester)
	// typically pass the layout's position in MuxSpec.Layouts so re-running
	// can cycle by reading the previous marker back via [Session.StatWindow].
	// Cycling itself is a consumer concern; muxctl provides only the
	// read/write primitives.
	//
	// ApplyLayout MUST NOT stop any external process; only the in-pane argv
	// from the previous build is torn down with the panes.
	ApplyLayout(ctx context.Context, root PaneSpec, marker int) (map[string]Pane, error)

	// Close closes the controlled window. As with ApplyLayout, closing MUST
	// NOT affect any process the panes were observing — the multiplexer is a
	// viewer, not a supervisor.
	Close(ctx context.Context) error

	// StatWindow inspects an arbitrary window in this driver's
	// server/session and returns the muxctl-recognized data read from its
	// panes' driver-recorded state (marker and pane names). windowID is the
	// driver's native window id (e.g. tmux "@7"). The queried window need NOT
	// be the Session's own controlled window — callers probe other windows
	// via this method to decide "is this someone else's muxctl window".
	StatWindow(ctx context.Context, windowID string) (WindowStat, error)
}

// Pane is the runtime identity of a realized pane returned by
// [Session.ApplyLayout]. It carries only what callers need to correlate,
// address, and report on panes after construction; it is not used to build
// them.
type Pane interface {
	// PaneId returns the multiplexer's pane id (e.g. tmux "%42"). Opaque
	// across drivers.
	PaneId() string

	// Name returns the pane name. It matches [PaneSpec.Name] and the map key
	// under which this Pane was returned from [Session.ApplyLayout].
	Name() string
}

// WindowStat is the muxctl-recognized data extracted from a window's
// external state via [Session.StatWindow]. All fields are best-effort:
// missing or unparseable data is zero-valued rather than errored.
type WindowStat struct {
	// Marker is the int recorded on the panes by [Session.ApplyLayout] (the
	// tmux driver stores it in a per-pane user option). -1 when no pane in
	// the window carries a marker, when panes disagree, or when the window
	// has no panes muxctl can recognize.
	Marker int

	// PaneNames are the pane names (the tmux driver reads them from the pane
	// border titles), so consumers can compare them against command names.
	// The slice is in tmux list-panes order, not muxctl pane-name order.
	PaneNames []string
}

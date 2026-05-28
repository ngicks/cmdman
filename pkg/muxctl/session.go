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
	// ApplyLayout MUST NOT stop any external process; only the in-pane argv
	// from the previous build is torn down with the panes.
	ApplyLayout(ctx context.Context, root PaneSpec) (map[string]Pane, error)

	// Close closes the controlled window. As with ApplyLayout, closing MUST
	// NOT affect any process the panes were observing — the multiplexer is a
	// viewer, not a supervisor.
	Close(ctx context.Context) error
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

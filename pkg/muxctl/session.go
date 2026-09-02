package muxctl

import "context"

// Session controls one window in one multiplexer session.
//
// A window carries two independent identities, and a Session controls it
// whichever of them it holds: the project that owns it (the
// [Config.OwnedIdentity] stamp) and the frame shown around it (the frame def
// name under [StateKeyFrameDef]). Either may be absent, and neither is the
// other's business. A single command invocation still owns exactly one window
// — the named entries in [MuxSpec.Layouts] are switchable configurations the
// user picks among via repeated calls to ApplyLayout, NOT separate windows,
// which is what distinguishes a cmdman mux session from a general multi-window
// dashboard — but the window is no longer that invocation's alone.
//
// The two identities divide the window's panes, and every operation is scoped
// to one side of that division:
//
//   - ApplyLayout rebuilds the project region only, inside whatever the frame
//     panes leave over.
//   - ShowFrame and HideFrame realize and remove frame panes only; what the
//     project region runs is never disturbed.
//   - Teardown is per side — Detach for the project, HideFrame for the frame —
//     and whichever call removes the LAST driver state restores the window
//     itself.
//   - Focus lands in the project region. A frame pane is never a focus
//     candidate, whichever operation settles the focus.
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
	// It RESETS the project region — the panes a previous apply built, plus
	// whatever was split into them — and rebuilds it inside what the window's
	// frame panes leave over; switching among MuxSpec.Layouts is done by
	// passing each layout's Root in turn. On an unframed window the project
	// region is the whole window, so this is the whole-window reset it has
	// always been. A framed window whose project region is gone — a frame
	// shown before anything was launched, or one that outlived its project —
	// has the region spawned back as the driver's default pane and the layout
	// built inside it. Returns the resulting runtime panes keyed by pane name
	// (PaneSpec.Name).
	//
	// A frame shown around the window is left alone: its panes are not
	// rebuilt, they carry no layout marker, they are never sent
	// [Config.ViewerDetachKeys], and they are never the pane left focused —
	// the focus root asks for through [Leaf.Focus], or the first leaf by
	// default, is chosen among root's own leaves.
	//
	// marker is an opaque non-negative integer the driver records on each
	// pane in driver-specific state (the tmux driver uses a per-pane user
	// option); muxctl does not interpret it. Pass marker < 0 to skip
	// recording it. Consumers (the cmdman mux family) typically pass the
	// layout's position in MuxSpec.Layouts so re-running can cycle by reading
	// the previous marker back via [Session.StatWindow].
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

	// WindowID returns the driver-native id of the controlled window (e.g.
	// tmux "@7"). Useful for callers that query the window via [StatWindow]
	// or address it outside the driver.
	WindowID() string

	// Detach tears the project region down to a single clean default pane and
	// removes the project state this Session installed: it gracefully detaches
	// the in-pane viewers (via [Config.ViewerDetachKeys]), collapses the
	// project's panes to one, and clears the ownership stamp so the window no
	// longer answers for that project. Like ApplyLayout and Close, Detach MUST
	// NOT stop any process the panes were observing — the viewers it tears
	// down are disposable. It is the explicit "I'm done with this dashboard,
	// give me my window back" operation, distinct from Close (which kills the
	// whole window).
	//
	// Teardown is per side. A frame shown around the project survives Detach
	// intact — its panes and its [StateKeyFrameDef] state are [HideFrame]'s to
	// remove — and the window is left framed and projectless, exactly as a
	// frame shown before anything was launched leaves it. Whichever of the two
	// teardowns removes the LAST driver state restores the window itself as
	// well, so neither side can leave a half-restored window behind. On a
	// window with no frame, Detach is that last teardown and restores the whole
	// window, as it always has.
	Detach(ctx context.Context) error

	// ShowFrame realizes the pane tree root around the content the window
	// already holds. The pane named mainName stands for that content — the
	// main region — and every other leaf in root becomes a frame pane docked
	// at the edge its position in root describes. The main region is only
	// RESIZED: its panes are never killed, rebuilt, or respawned, so what they
	// display keeps running. A window with no main region of its own frames
	// the driver's default pane — whether it never had a layout applied or the
	// one it had has since exited — so a frame can be shown before anything is
	// launched and re-shown after everything has gone.
	//
	// mainName may name a placeholder that carries no [Leaf.Cmd]: it stands
	// for panes that already exist, so nothing is ever spawned for it.
	//
	// Frame panes are recorded as such in driver state, which is what keeps
	// them out of the project layout: [Session.ApplyLayout] rebuilds around
	// them and they carry no layout marker. ShowFrame leaves the focus in the
	// main region, never in a frame pane.
	//
	// defName names the frame being shown and is stored in the window's
	// [StateKeyFrameDef] state, which is what makes the window discoverable as
	// framed; both mainName and defName are required. As with ApplyLayout,
	// ShowFrame MUST NOT stop any external process.
	ShowFrame(ctx context.Context, root PaneSpec, mainName, defName string) error

	// HideFrame removes every frame pane [Session.ShowFrame] realized and
	// clears the [StateKeyFrameDef] state, letting the main region expand into
	// the whole window. The main region's panes are left untouched, so hiding
	// a frame disturbs nothing the window was showing; hiding a window that
	// carries no frame is a no-op.
	//
	// All frame panes are treated alike: whether a frame pane's command is
	// disposable or wants preserving is settled by the consumer before it
	// calls HideFrame. Selecting or cycling frames is HideFrame followed by
	// ShowFrame.
	//
	// Which panes are the frame's is the driver's own record, never the
	// [StateKeyFrameDef] value: a driver MUST take down the frame panes of a
	// window whose recorded def has gone empty or stale. That is what makes
	// hiding the way back from a teardown that died partway, and it is why a
	// consumer may call HideFrame on a window it cannot name a frame for.
	//
	// HideFrame is the frame side's teardown, the counterpart to [Detach]: it
	// clears the frame's state and nothing else, and it never changes what the
	// main region runs. When the frame was the last driver state on the window,
	// HideFrame restores the window itself as well — including the case where
	// the frame was all the window held, which it leaves as one default pane
	// rather than as no window at all.
	HideFrame(ctx context.Context) error

	// RespawnLeaf quiesces any in-pane viewer for paneID (via
	// [Config.ViewerDetachKeys]), then stamps leaf's title/state and respawns
	// the pane with leaf's command. It is the targeted single-pane counterpart
	// to ApplyLayout: consumers advance one visible pane to a new command
	// without rebuilding the whole window, preserving the window's layout
	// marker. paneID MUST belong to this Session's controlled window.
	RespawnLeaf(ctx context.Context, paneID string, leaf Leaf) error
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
	//
	// Only the project region votes: a frame's panes are not part of the
	// layout the marker indexes, so they neither supply a marker nor break the
	// agreement of the panes that do. A framed window therefore reports the
	// layout it is showing, exactly as an unframed one does. Likewise a pane
	// the driver never stamped (a shell the user split off, a floating pane
	// joined into the window) is foreign and stays out of the vote.
	Marker int

	// PaneNames are the pane names (the tmux driver reads them from the pane
	// border titles), so consumers can compare them against command names.
	// The slice is in tmux list-panes order, not muxctl pane-name order.
	PaneNames []string
}

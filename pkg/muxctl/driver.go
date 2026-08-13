package muxctl

import (
	"context"
	"fmt"
	"sync"
)

// Config selects the window controlled by a new multiplexer [Session].
type Config struct {
	// SessionName is required unless WindowID is set.
	SessionName string

	// WindowName is required when WindowID is empty.
	WindowName string

	// WindowID directly targets an existing driver-native window.
	WindowID string

	// OwnedIdentity is an opaque durable stamp used by [Server.ListWindows].
	// Empty disables stamping.
	OwnedIdentity string

	// ReuseCurrentWindow permits taking over the caller's current window when
	// WindowID is empty; otherwise the server finds or creates WindowName.
	//
	// Only a window that is safe to take over is: one already stamped with this
	// same OwnedIdentity (so a re-run cycles in place), one holding a frame but
	// no project (chrome put up before any project — the project lands in the
	// region the frame leaves over), or an unowned one whose repurposing
	// clobbers nothing. A window owned by another identity is never taken over:
	// applying a layout would rebuild its region out from under it.
	ReuseCurrentWindow bool

	// StartDirectory is the working directory the shell of a newly created
	// session/window starts in. It applies to creation only: a window that
	// already exists is never moved, so a caller may pass it unconditionally.
	// Empty leaves the multiplexer's own default.
	StartDirectory string

	// ViewerDetachKeys gracefully detach old in-pane viewers before teardown.
	// They must match the sequence the viewers honor; empty disables detaching.
	ViewerDetachKeys []string
}

// StateKey names a per-window state slot addressed by [Server.ReadWindowState],
// [Server.WriteWindowState], and [ListOptions.StateKeys]. A StateKey MUST be a
// short [A-Za-z0-9_-] token: drivers splice it into their native identifiers
// unescaped (the tmux driver forms the window option "@cmdman_<key>"), so it is
// a closed, code-declared vocabulary — not arbitrary external input.
type StateKey string

// State keys are interpreted by their consumers, not drivers.
const (
	// StateKeyScale holds the per-command cycle-scale replica positions.
	StateKeyScale StateKey = "scale"

	// StateKeyFrameDef holds the name of the frame currently shown around the
	// window, as passed to [Session.ShowFrame]. A non-empty value means the
	// window is framed; [Session.HideFrame] clears it.
	StateKeyFrameDef StateKey = "frame_def"
)

// ListOptions controls window enumeration and state access.
type ListOptions struct {
	// Session restricts scans to one session; empty scans the server.
	Session string

	// Identity filters ownership stamps exactly; empty returns every window
	// carrying cmdman state, held by either side. It matches the ownership
	// slot alone: a window shown
	// a frame of the same name is not thereby a window of that project. So
	// "the windows of project P" is this filter — framed or not, since a frame
	// leaves the ownership stamp alone — and "the framed windows" is an
	// unfiltered scan read through [Window.Frame].
	Identity string

	// StateKeys lists the per-window state slots [Server.ListWindows] fetches
	// inline into each [Window.State], so callers avoid a round-trip per
	// window. Empty leaves each row's State nil. See [StateKey] for the token
	// constraint the driver relies on.
	StateKeys []StateKey
}

// Window is a row returned by [Server.ListWindows]: one window that carries
// cmdman state — an ownership stamp, a shown frame, or both.
type Window struct {
	SessionName string

	WindowID string

	// WindowName is the human-visible window name. It may differ from Identity
	// — a takeover window keeps its original name while the stamp records the
	// cmdman-assigned ownership value.
	WindowName string

	// Identity is the opaque [Config.OwnedIdentity] stamp.
	Identity string

	// Frame is the name of the frame def shown around the window — the
	// [StateKeyFrameDef] value [Session.ShowFrame] wrote — and "" when the
	// window is unframed. It is the window's other identity: Identity says
	// which project the window holds, Frame says which frame surrounds it, and
	// a window carrying either one alone is still enumerated. The two are
	// independent, so a framed window answers a query for its project exactly
	// as an unframed one does.
	Frame string

	// Marker is the layout index last applied to the window (see
	// [Session.StatWindow]), or -1 when no layout has been applied yet or the
	// panes carry inconsistent markers.
	Marker int

	// State holds the per-window state values for the keys requested via
	// [ListOptions.StateKeys], keyed by those keys. A requested key whose
	// value is unset maps to "". Nil when no StateKeys were requested.
	State map[StateKey]string
}

// FocusOptions configures [Server.FocusWindow].
type FocusOptions struct {
	// SwitchClient permits moving a client to the focused window's session when
	// the window lives elsewhere. A caller outside the multiplexer leaves it
	// false: it has no client of its own, so switching would yank somebody
	// else's terminal instead of landing its own.
	SwitchClient bool

	// ClientSession names the session the caller itself is sitting in; the
	// driver moves that session's client. Empty falls back to the sole attached
	// client and moves nobody when several are attached — multiplexers resolve
	// "the current client" from the caller's terminal, and a cmdman process
	// summoned in a tmux popup has none (measured on a scratch server: tmux
	// picked a different client than the one showing the popup).
	ClientSession string
}

// ServerConfig selects which multiplexer server a [Driver] binds to. Executable
// and Socket name concepts common to every multiplexer, so they are first-class
// fields here rather than well-known keys buried in the DriverOpt bag.
type ServerConfig struct {
	// Executable is the multiplexer binary to run. Empty selects the driver's
	// default binary (the tmux driver runs "tmux").
	Executable string

	// Socket selects the server instance. Empty binds to the driver's default
	// server; how a non-empty value is resolved is driver-defined (the tmux
	// driver treats a value containing a path separator as a socket file path
	// and a bare name as a named socket in tmux's default directory).
	Socket string

	// DriverOpt carries genuinely driver-specific keys only — Executable and
	// Socket are named fields, not entries here. The tmux driver defines no
	// such keys today.
	DriverOpt map[string]string
}

// Driver binds to one multiplexer backend selected by a [ServerConfig].
// A driver package registers itself under a name in its init function via
// [RegisterDriver] (the database/sql idiom); the composition root blank-imports
// each driver it wants linked and retrieves it with [LookupDriver].
//
// The contract is three tiers: a Driver [Driver.Connect]s to a [Server], the
// server builds and enumerates cmdman-owned windows, and a [Session] controls
// one such window. Server addressing is captured once at Connect time, so the
// session-less operations on [Server] no longer thread it per call.
type Driver interface {
	// Connect binds to the server selected by cfg. It is a pure binding — no
	// I/O, no server spawn — so a missing server surfaces later as zero rows
	// from [Server.ListWindows] or ok=false from [Server.Open], never as a
	// Connect error.
	Connect(ctx context.Context, cfg ServerConfig) (Server, error)
}

// Server is one multiplexer server: it builds, enumerates, and stores
// per-window state for cmdman-owned windows on that server. A Server is bound
// once by [Driver.Connect]; its session-less capabilities (constructors,
// enumeration, leaf-find, per-window state) must work from any calling context
// with no attached session, while the per-session operations live on [Session].
type Server interface {
	// New builds (or finds-or-creates) the cmdman-owned window described by
	// cfg and returns a Session controlling it. It applies no layout — call
	// [Session.ApplyLayout] to populate the window.
	New(ctx context.Context, cfg Config) (Session, error)

	// Open locates an already-existing cmdman-owned window WITHOUT creating one
	// and WITHOUT mutating window state. It NEVER creates a session or a
	// window, never sets or clears any window option, and never accepts a
	// current window the caller does not own — neither an unowned one nor
	// another identity's (unlike New, which may repurpose a current window that
	// is safe to take over). ok is false (with a nil error and nil Session)
	// when no such window is found, so teardown callers can no-op instead of
	// building a stray window.
	Open(ctx context.Context, cfg Config) (Session, bool, error)

	// ListWindows enumerates windows carrying cmdman state — an ownership
	// stamp, a shown frame, or both — server-wide (or restricted to
	// opts.Session), optionally filtered to an exact opts.Identity. Each row
	// reports both identities ([Window.Identity], [Window.Frame]), so a window
	// held by neither side alone is still discoverable by the side that holds
	// it. The scan MUST NOT depend on an attached client or the current
	// window. A missing server (or a named-but-absent session) yields zero
	// rows, not an error. Each returned row's State is filled for the keys
	// named in opts.StateKeys.
	ListWindows(ctx context.Context, opts ListOptions) ([]Window, error)

	// FindPane returns the id of the pane in windowID that tracks key — the
	// pane whose realized leaf recorded [Leaf.CycleKey] == key. Every runtime
	// pane corresponds to a spec leaf (containers are spec-only and never
	// realize a pane), so unstamped placeholder panes simply never match. ok
	// is false (with a nil error) when no pane carries the key.
	FindPane(ctx context.Context, windowID, key string) (paneID string, ok bool, err error)

	// ReadWindowState reads the opaque per-window state value stored under key
	// on windowID. An absent key reads as "" with a nil error. The server maps
	// key onto its own native per-window storage (the tmux driver forms the
	// window option "@cmdman_<key>"); muxctl interprets neither the key nor the
	// value (encoding is the caller's concern). See [StateKey] for the token
	// constraint.
	ReadWindowState(ctx context.Context, windowID string, key StateKey) (string, error)

	// WriteWindowState stores value under key on windowID. An empty value
	// unsets the key entirely, leaving no stale value behind. As with
	// ReadWindowState the key and value are opaque to muxctl.
	WriteWindowState(ctx context.Context, windowID string, key StateKey, value string) error

	// FocusWindow puts windowID in front: it makes the window the current one
	// inside its own session and, when opts.SwitchClient is set, moves the
	// caller's client to that session. It is the landing primitive — "you are
	// looking at this window now" — and mutates no window state: no layout, no
	// ownership stamp, no per-window state slot.
	//
	// With no client to move (nothing attached, or none opts.ClientSession
	// identifies) the window is still selected and the call succeeds: selecting
	// is all focus can mean with nobody watching, and whether to hand the
	// caller's own terminal over instead is the caller's decision (see
	// [Server.AttachCommand]).
	FocusWindow(ctx context.Context, windowID string, opts FocusOptions) error

	// AttachCommand returns the argv that gives the calling terminal to
	// sessionName on this server (tmux: `tmux [-L socket] attach-session -t
	// =<name>`). It is what a caller outside the multiplexer runs after
	// FocusWindow selected the window: with no client attached there is nothing
	// to switch, so the terminal has to become one. It builds a command line and
	// performs no I/O.
	AttachCommand(sessionName string) []string

	// CurrentSessionName reports the name of the multiplexer session the
	// calling terminal is attached to ("inside the multiplexer"), with ok
	// false when the caller is not attached or the session is undetectable. It
	// returns a session name, not a [Session]: it opens nothing.
	CurrentSessionName(ctx context.Context) (name string, ok bool, err error)

	// CurrentWindowID reports the driver-native id (e.g. tmux "@7") of the
	// window the caller is looking at: the current window of session when that
	// is non-empty, else of the session the calling terminal is attached to. It
	// is the "which window am I in" query [CurrentSessionName] is the session
	// half of — the addressing a per-window fixture needs, since a window the
	// caller merely sits in carries no name or identity to find it by.
	//
	// ok is false with a nil error when there is no such window — no server, no
	// such session, or no session named and no attached client to ask — because
	// "not inside the multiplexer" is a caller's decision to make, not a
	// failure. Like the other session-less queries it opens nothing and mutates
	// no state.
	CurrentWindowID(ctx context.Context, session string) (id string, ok bool, err error)
}

var (
	driversMu sync.RWMutex
	drivers   = map[string]Driver{}
)

// RegisterDriver makes d available under name. It is meant for a driver
// package's init function; the composition root blank-imports the driver
// package to link it. RegisterDriver panics if d is nil or name is already
// registered — both are init-time programming errors, not runtime conditions.
func RegisterDriver(name string, d Driver) {
	driversMu.Lock()
	defer driversMu.Unlock()
	if d == nil {
		panic("muxctl: RegisterDriver driver is nil")
	}
	if _, dup := drivers[name]; dup {
		panic("muxctl: RegisterDriver called twice for driver " + name)
	}
	drivers[name] = d
}

// LookupDriver returns the driver registered under name, or an error naming
// the missing driver (the usual cause is a forgotten blank import of the
// driver's package at the composition root).
func LookupDriver(name string) (Driver, error) {
	driversMu.RLock()
	d, ok := drivers[name]
	driversMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf(
			"muxctl: no driver registered for %q (missing blank import of its package?)",
			name,
		)
	}
	return d, nil
}

package muxctl

import (
	"context"
	"fmt"
	"sync"
)

// Config configures a multiplexer [Session] built by a [Driver].
//
// Every field except DriverOpt is driver-agnostic; DriverOpt carries the
// per-driver knobs (the tmux driver honors "path" and "socket"). A driver
// resolves its target window either from SessionName+WindowName
// (find-or-create by name) or from WindowID (target an existing window
// directly). Constructing a Session applies no layout — call
// [Session.ApplyLayout] to populate the window.
type Config struct {
	// SessionName names the multiplexer session this driver targets. Required
	// unless WindowID is set (the window already identifies its session). When
	// required, the session is created if it does not already exist; a driver
	// never kills a session it did not create.
	SessionName string

	// WindowName names the cmdman-owned window within SessionName. Required
	// when WindowID is empty (find-or-create by name); ignored when WindowID
	// is set. The driver operates exclusively on the resolved window; sibling
	// windows are left untouched.
	WindowName string

	// WindowID, when non-empty, targets the existing window with this
	// driver-native id (e.g. tmux "@7") and skips find-or-create by
	// WindowName. Used by callers that pick the target window themselves —
	// e.g. "apply to the caller's current window" or "create a fresh window
	// per invocation and target it." When set, SessionName/WindowName are not
	// consulted.
	WindowID string

	// OwnedIdentity, when non-empty, is stamped onto the resolved window as
	// the durable ownership signal enumerated by [Driver.ListWindows].
	// It survives pane churn, manual pane splits, and window renames. Callers
	// supply an opaque string (e.g. a workdir-hash+project prefix for compose,
	// or the window name for standalone mux); the driver stores and returns it
	// verbatim, never interpreting it. Empty disables stamping — useful for
	// one-off callers that do not need enumeration.
	OwnedIdentity string

	// ReuseCurrentWindow, when true and WindowID is empty, applies the layout
	// to the caller's current window instead of a window found-or-created by
	// name — but only when that current window is safe to take over (it is
	// already cmdman-owned, already named WindowName, or has a single pane).
	// When the current window cannot be resolved or is not safe to reuse, the
	// driver falls back to find-or-create by name. Callers set this when
	// running inside a multiplexer client without an explicit session.
	ReuseCurrentWindow bool

	// ViewerDetachKeys is the send-keys key sequence (in driver syntax; e.g.
	// tmux {"C-p", "C-q"}) that [Session.ApplyLayout], [Session.Detach], and
	// [Session.RespawnLeaf] send to the in-pane viewers of a previous build to
	// make them detach gracefully before the panes are torn down and rebuilt.
	// It MUST match the detach sequence those viewers actually honor. Empty
	// disables graceful detach — the old panes are killed directly, which
	// SIGKILLs the in-pane processes mid-frame.
	ViewerDetachKeys []string

	// DriverOpt carries driver-specific options that have no driver-agnostic
	// meaning. The tmux driver honors "path" (the tmux binary path, default
	// "tmux") and "socket" (the -L socket name; empty uses tmux's default
	// socket, or the server inherited from $TMUX — a non-empty socket selects
	// a dedicated server, the opt-in isolation mode).
	DriverOpt map[string]string
}

// StateKey names a per-window state slot addressed by [Driver.ReadWindowState],
// [Driver.WriteWindowState], and [ListOptions.StateKeys]. A StateKey MUST be a
// short [A-Za-z0-9_-] token: drivers splice it into their native identifiers
// unescaped (the tmux driver forms the window option "@cmdman_<key>"), so it is
// a closed, code-declared vocabulary — not arbitrary external input.
type StateKey string

// The closed vocabulary of per-window state slots. Drivers persist and return
// these verbatim, never interpreting them; the semantics and wire encoding of
// each slot's value are owned by the consuming layer (e.g. pkg/cmdman/mux owns
// the "scale" codec), keeping that vocabulary out of muxctl.
const (
	// StateKeyScale holds the per-command cycle-scale replica positions.
	StateKeyScale StateKey = "scale"
)

// ListOptions controls enumeration and per-window state access on a [Driver]:
// [Driver.ListWindows], [Driver.FindPane], [Driver.ReadWindowState], and
// [Driver.WriteWindowState]. Unlike [Config] it names no window to build; it
// selects the server (via DriverOpt) and, for listing, filters the results.
type ListOptions struct {
	// Session, when non-empty, restricts a scan to that session only. Empty
	// scans all sessions on the server.
	Session string

	// Identity, when non-empty, filters [Driver.ListWindows] to windows whose
	// ownership stamp equals this string exactly. Empty returns every stamped
	// window regardless of identity.
	Identity string

	// StateKeys lists the per-window state slots [Driver.ListWindows] fetches
	// inline into each [Window.State], so callers avoid a round-trip per
	// window. Empty leaves each row's State nil. See [StateKey] for the token
	// constraint the driver relies on.
	StateKeys []StateKey

	// DriverOpt carries driver-specific options, matching [Config.DriverOpt]
	// (the tmux driver honors "path" and "socket"). It MUST match the options
	// used when the dashboard was built for enumeration to find it.
	DriverOpt map[string]string
}

// Window is a row returned by [Driver.ListWindows]: one window that carries a
// cmdman ownership stamp.
type Window struct {
	// SessionName is the multiplexer session the window belongs to.
	SessionName string

	// WindowID is the driver-native window id (e.g. tmux "@3").
	WindowID string

	// WindowName is the human-visible window name. It may differ from Identity
	// — a takeover window keeps its original name while the stamp records the
	// cmdman-assigned ownership value.
	WindowName string

	// Identity is the value stamped via [Config.OwnedIdentity]. The driver
	// stores and returns it verbatim; upper layers interpret it.
	Identity string

	// Marker is the layout index last applied to the window (see
	// [Session.StatWindow]), or -1 when no layout has been applied yet or the
	// panes carry inconsistent markers.
	Marker int

	// State holds the per-window state values for the keys requested via
	// [ListOptions.StateKeys], keyed by those keys. A requested key whose
	// value is unset maps to "". Nil when no StateKeys were requested.
	State map[StateKey]string
}

// Driver is a multiplexer backend: it builds and enumerates cmdman-owned
// windows without owning any multiplexer state of its own. A driver package
// registers itself under a name in its init function via [RegisterDriver]
// (the database/sql idiom); the composition root blank-imports each driver it
// wants linked and retrieves it with [LookupDriver].
//
// The session-less capabilities (constructors, enumeration, leaf-find,
// per-window state) live on Driver because they must work from any calling
// context with no attached session; the per-session operations live on
// [Session].
type Driver interface {
	// New builds (or finds-or-creates) the cmdman-owned window described by
	// cfg and returns a Session controlling it. It applies no layout — call
	// [Session.ApplyLayout] to populate the window.
	New(ctx context.Context, cfg Config) (Session, error)

	// Open locates an already-existing cmdman-owned window WITHOUT creating one
	// and WITHOUT mutating window state. It NEVER creates a session or a
	// window, never sets or clears any window option, and never takes over an
	// unowned current window (unlike New, which may repurpose a safe current
	// window). ok is false (with a nil error and nil Session) when no such
	// window is found, so teardown callers can no-op instead of building a
	// stray window.
	Open(ctx context.Context, cfg Config) (Session, bool, error)

	// ListWindows enumerates windows carrying a cmdman ownership stamp,
	// server-wide (or restricted to opts.Session), optionally filtered to an
	// exact opts.Identity. The scan MUST NOT depend on an attached client or
	// the current window. A missing server (or a named-but-absent session)
	// yields zero rows, not an error. Each returned row's State is filled for
	// the keys named in opts.StateKeys.
	ListWindows(ctx context.Context, opts ListOptions) ([]Window, error)

	// FindPane returns the id of the pane in windowID that tracks key — the
	// pane whose realized leaf recorded [Leaf.CycleKey] == key. Every runtime
	// pane corresponds to a spec leaf (containers are spec-only and never
	// realize a pane), so unstamped placeholder panes simply never match. ok
	// is false (with a nil error) when no pane carries the key.
	FindPane(
		ctx context.Context,
		opts ListOptions,
		windowID, key string,
	) (paneID string, ok bool, err error)

	// ReadWindowState reads the opaque per-window state value stored under key
	// on windowID. An absent key reads as "" with a nil error. The driver maps
	// key onto its own native per-window storage (the tmux driver forms the
	// window option "@cmdman_<key>"); muxctl interprets neither the key nor the
	// value (encoding is the caller's concern). See [StateKey] for the token
	// constraint.
	ReadWindowState(
		ctx context.Context,
		opts ListOptions,
		windowID string,
		key StateKey,
	) (string, error)

	// WriteWindowState stores value under key on windowID. An empty value
	// unsets the key entirely, leaving no stale value behind. As with
	// ReadWindowState the key and value are opaque to muxctl.
	WriteWindowState(
		ctx context.Context,
		opts ListOptions,
		windowID string,
		key StateKey,
		value string,
	) error
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

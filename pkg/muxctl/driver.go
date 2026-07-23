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

	// OwnedIdentity is an opaque durable stamp used by [Driver.ListWindows].
	// Empty disables stamping.
	OwnedIdentity string

	// ReuseCurrentWindow permits taking over a safe current window when
	// WindowID is empty; otherwise the driver finds or creates WindowName.
	ReuseCurrentWindow bool

	// ViewerDetachKeys gracefully detach old in-pane viewers before teardown.
	// They must match the sequence the viewers honor; empty disables detaching.
	ViewerDetachKeys []string

	// DriverOpt carries driver-specific options.
	DriverOpt map[string]string
}

// StateKey names a per-window state slot addressed by [Driver.ReadWindowState],
// [Driver.WriteWindowState], and [ListOptions.StateKeys]. A StateKey MUST be a
// short [A-Za-z0-9_-] token: drivers splice it into their native identifiers
// unescaped (the tmux driver forms the window option "@cmdman_<key>"), so it is
// a closed, code-declared vocabulary — not arbitrary external input.
type StateKey string

// State keys are interpreted by their consumers, not drivers.
const (
	// StateKeyScale holds the per-command cycle-scale replica positions.
	StateKeyScale StateKey = "scale"
)

// ListOptions controls window enumeration and state access.
type ListOptions struct {
	// Session restricts scans to one session; empty scans the server.
	Session string

	// Identity filters ownership stamps exactly; empty returns all stamped windows.
	Identity string

	// StateKeys lists the per-window state slots [Driver.ListWindows] fetches
	// inline into each [Window.State], so callers avoid a round-trip per
	// window. Empty leaves each row's State nil. See [StateKey] for the token
	// constraint the driver relies on.
	StateKeys []StateKey

	// DriverOpt must select the same server used to build the window.
	DriverOpt map[string]string
}

// Window is a row returned by [Driver.ListWindows]: one window that carries a
// cmdman ownership stamp.
type Window struct {
	SessionName string

	WindowID string

	// WindowName is the human-visible window name. It may differ from Identity
	// — a takeover window keeps its original name while the stamp records the
	// cmdman-assigned ownership value.
	WindowName string

	// Identity is the opaque [Config.OwnedIdentity] stamp.
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

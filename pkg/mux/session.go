// Package mux defines interfaces for terminal multiplexer providers.
package mux

import "context"

// Session represents a terminal multiplexer session containing windows.
type Session interface {
	Id() string
	Name(ctx context.Context) (string, error)
	// StartupKeys returns session-level keys sent to every new pane before window-level keys.
	StartupKeys() []string
	// NewWindow creates a new window in the session.
	// startupKeys are window-level keys sent to every new pane after session-level keys.
	NewWindow(ctx context.Context, name string, startupKeys []string) (Window, error)
	GetAt(ctx context.Context, i int) (Window, error)
	GetById(ctx context.Context, id string) (Window, error)
	List(ctx context.Context) ([]Window, error)
	Close(ctx context.Context) error
}

// Window represents a window within a session, containing one or more panes.
type Window interface {
	Id() string
	Index(ctx context.Context) (int, error)
	Name(ctx context.Context) (string, error)
	// StartupKeys returns window-level keys sent to every new pane after session-level keys.
	StartupKeys() []string
	// Split splits the window into n additional panes.
	// After Split returns, the window contains its current pane count plus n panes.
	Split(ctx context.Context, n int) error
	List(ctx context.Context) ([]Pane, error)
	GetAt(ctx context.Context, i int) (Pane, error)
	GetById(ctx context.Context, id string) (Pane, error)
	Close(ctx context.Context) error
}

// Pane represents a single terminal pane.
type Pane interface {
	Id() string
	Index(ctx context.Context) (int, error)
	Name(ctx context.Context) (string, error)
	SendKeys(ctx context.Context, keys []string) error
	Capture(ctx context.Context, from int, limit int) (string, error)
	Close(ctx context.Context) error
}

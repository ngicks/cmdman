package tmux

import (
	"context"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// Driver is the tmux implementation of [muxctl.Driver]. It self-registers under
// the name "tmux" in init, so importing this package for its side effects
// (a blank import at the composition root) links the driver into the binary.
// Its methods are thin adapters over the package-level tmux functions,
// returning the concrete *Session as a [muxctl.Session].
type Driver struct{}

var _ muxctl.Driver = Driver{}

func init() {
	muxctl.RegisterDriver("tmux", Driver{})
}

// New implements [muxctl.Driver.New].
func (Driver) New(ctx context.Context, cfg muxctl.Config) (muxctl.Session, error) {
	s, err := New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Open implements [muxctl.Driver.Open]. It returns a nil [muxctl.Session] (not
// a typed-nil interface) when no window is found, so callers can compare the
// result against nil.
func (Driver) Open(ctx context.Context, cfg muxctl.Config) (muxctl.Session, bool, error) {
	s, ok, err := Open(ctx, cfg)
	if s == nil {
		return nil, ok, err
	}
	return s, ok, err
}

// ListWindows implements [muxctl.Driver.ListWindows].
func (Driver) ListWindows(
	ctx context.Context,
	opts muxctl.ListOptions,
) ([]muxctl.Window, error) {
	return ListWindows(ctx, opts)
}

// FindPane implements [muxctl.Driver.FindPane].
func (Driver) FindPane(
	ctx context.Context,
	opts muxctl.ListOptions,
	windowID, key string,
) (string, bool, error) {
	return FindPane(ctx, opts, windowID, key)
}

// ReadWindowState implements [muxctl.Driver.ReadWindowState].
func (Driver) ReadWindowState(
	ctx context.Context,
	opts muxctl.ListOptions,
	windowID string,
	key muxctl.StateKey,
) (string, error) {
	return ReadWindowState(ctx, opts, windowID, key)
}

// WriteWindowState implements [muxctl.Driver.WriteWindowState].
func (Driver) WriteWindowState(
	ctx context.Context,
	opts muxctl.ListOptions,
	windowID string,
	key muxctl.StateKey,
	value string,
) error {
	return WriteWindowState(ctx, opts, windowID, key, value)
}

package tmux

import (
	"context"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// Driver is the tmux implementation of [muxctl.Driver]. It self-registers under
// the name "tmux" in init, so importing this package for its side effects
// (a blank import at the composition root) links the driver into the binary.
// Connect is its only method: it binds addressing to a [Server], which owns the
// session-less operations and constructs the [Session] values.
type Driver struct{}

var _ muxctl.Driver = Driver{}

func init() {
	muxctl.RegisterDriver("tmux", Driver{})
}

// Connect implements [muxctl.Driver.Connect]. It binds to the tmux server
// selected by cfg — cfg.Executable overrides the tmux binary (default "tmux")
// and cfg.Socket selects the server (see the executor's socket handling: a path
// value maps to -S, a bare name to -L) — and returns a [Server] sharing one
// executor. It runs no tmux command: a missing server surfaces later as zero
// rows from [Server.ListWindows] or ok=false from [Server.Open], never as a
// Connect error.
//
// cfg.DriverOpt is accepted and ignored: the tmux driver defines no
// driver-specific keys today (the former "path"/"socket" keys are now the
// first-class [muxctl.ServerConfig] fields Executable and Socket).
func (Driver) Connect(_ context.Context, cfg muxctl.ServerConfig) (muxctl.Server, error) {
	return &Server{exec: newExecutor(cfg.Executable, cfg.Socket)}, nil
}

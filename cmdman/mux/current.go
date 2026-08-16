package mux

import (
	"context"
	"fmt"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// CurrentWindowOptions configures [CurrentWindowID].
type CurrentWindowOptions struct {
	// Driver selects and configures the multiplexer server exactly as it does
	// for [List]: an empty Driver.Name autodetects from Env ($TMUX > $ZELLIJ >
	// tmux).
	Driver muxctl.DriverSpec
	// SessionName, when non-empty, asks for that session's current window
	// instead of the one the calling terminal is attached to.
	SessionName string
	// Env is the process env consulted for driver autodetection. Empty defaults
	// to os.Environ().
	Env []string
}

// CurrentWindowID reports the driver-native id (e.g. tmux "@7") of the window
// the caller is looking at. It is a thin layer over
// [muxctl.Server.CurrentWindowID], as [List] is over ListWindows, so callers
// outside cmdman/mux can ask "which window am I in" without reaching for a
// driver-private type.
//
// ok=false with a nil error means there is no such window. Callers must NOT
// read ok=true as proof of an enclosing window: the answer is client-relative,
// not process-relative, so a caller outside the multiplexer still gets some
// session's current window. Guard the call on $TMUX/$ZELLIJ the way the frame
// verbs do (see frameWindowID).
func CurrentWindowID(ctx context.Context, opts CurrentWindowOptions) (string, bool, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}

	server, err := resolveServer(ctx, opts.Driver, env)
	if err != nil {
		return "", false, err
	}

	id, ok, err := server.CurrentWindowID(ctx, opts.SessionName)
	if err != nil {
		return "", false, fmt.Errorf("mux: resolve the current window: %w", err)
	}
	return id, ok, nil
}

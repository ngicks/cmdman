package mux

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// DetachOptions configures [Detach]. It mirrors the subset of [RunOptions] that
// teardown needs: Detach builds no panes, so it requires no spec leaves — only
// enough to locate the same driver, server, session and window that [Run]
// targeted.
type DetachOptions struct {
	// Driver selects the multiplexer driver. Empty autodetects from Env the
	// same way [Run] does ($TMUX > $ZELLIJ > tmux).
	Driver string
	// DriverOpt carries driver-specific options; the tmux driver honors "path"
	// and "socket". A dashboard built on a non-default socket can only be found
	// when the same socket is supplied here.
	DriverOpt map[string]string
	// SessionName names the multiplexer session to target. Empty resolves the
	// same way as [RunOptions.SessionName] (current tmux session, else
	// "cmdman").
	SessionName string
	// WindowName names the cmdman-owned window. Empty defaults to SessionName,
	// matching [Run].
	WindowName string
	// Env is the process env consulted for driver autodetection and the
	// current-window takeover decision. Empty defaults to os.Environ().
	Env []string
	// Stdout is where the "nothing to detach" note is printed. Empty defaults
	// to os.Stdout.
	Stdout io.Writer
}

// Detach tears down the cmdman-owned window a prior [Run] built: it gracefully
// detaches the in-pane viewers, collapses the window to a single clean shell
// pane, and unsets the tmux options Run/New installed (pane-border-status and
// the per-pane @cmdman_marker). The supervised commands keep running in the
// daemon — only the disposable viewers are torn down.
//
// Detach never creates a window: when no cmdman dashboard window is found (no
// session, no matching window, or — inside tmux — the current window is not a
// marked dashboard), it prints a short note to opts.Stdout and returns nil.
//
// v1 supports the tmux driver only; any other resolved driver returns a "not
// implemented" error, mirroring [Run].
func Detach(ctx context.Context, opts DetachOptions) error {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	driver := resolveDriver(opts.Driver, env)
	if driver != "tmux" {
		return fmt.Errorf(
			"mux: driver %q is not implemented yet (v1 ships tmux only)", driver,
		)
	}

	path, socket := opts.DriverOpt["path"], opts.DriverOpt["socket"]
	explicitSession := opts.SessionName != ""
	sessionName := resolveSessionName(
		opts.SessionName,
		env,
		func() (string, error) { return currentTmuxSession(ctx, path, socket) },
	)
	windowName := opts.WindowName
	if windowName == "" {
		windowName = sessionName
	}

	// With no explicit --session, take over the caller's current window only
	// when it is a marked dashboard (OpenExisting enforces the marker check);
	// an explicit session means "target that session's named window".
	reuseCurrent := !explicitSession && envOf(env, "TMUX") != ""

	sess, ok, err := tmux.OpenExisting(ctx, tmux.Config{
		Path:               path,
		Socket:             socket,
		SessionName:        sessionName,
		WindowName:         windowName,
		ReuseCurrentWindow: reuseCurrent,
		ViewerDetachKeys:   viewerDetachKeys,
	})
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintf(
			stdout,
			"No cmdman dashboard window found to detach in session %q\n",
			sessionName,
		)
		return nil
	}
	return sess.Detach(ctx)
}

package mux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// DownOptions configures [Down].
type DownOptions struct {
	// Driver selects and configures the multiplexer server. An empty Driver.Name
	// autodetects from Env the same way [Run] does ($TMUX > $ZELLIJ > tmux);
	// Path/Socket/Opts feed [muxctl.Driver.Connect] to bind the server. A
	// dashboard built on a non-default socket can only be found when the same
	// Socket is supplied here.
	Driver muxctl.DriverSpec
	// SessionName, when non-empty, narrows the scan to that session only.
	// It is a pure filter passed to [muxctl.Server.ListWindows]; it does NOT
	// participate in identity derivation (only the identity defaulting path
	// uses the resolved session name). An explicit --session is therefore
	// optional for teardown: omitting it restores every matching dashboard
	// server-wide.
	SessionName string
	// WindowName is used solely for identity derivation when Identity is empty —
	// the standalone-mux default, the only caller that leaves Identity unset. It
	// is NOT used as a session-filter fallback. Empty defaults to the resolved
	// session name, exactly as [Run] does.
	WindowName string
	// Identity is the opaque ownership string passed to [muxctl.Server.ListWindows]
	// as the filter. When empty, it is derived the same way [Run] defaults it:
	// resolveSessionName → windowName default → identity = windowName. That
	// derivation is session-local, which is the standalone-mux limitation: a
	// dashboard built with the default naming in a different session resolves a
	// different identity. Compose callers always pass Identity explicitly —
	// unnamed projects included, since the work directory alone identifies one —
	// which eliminates the context-dependence entirely.
	Identity string
	// Env is the process env consulted for driver autodetection. Empty defaults
	// to os.Environ().
	Env []string
	// KillCreated closes the windows cmdman created outright instead of
	// restoring them, so a teardown leaves no shell pane and no frame behind —
	// nothing of the window at all. Windows cmdman borrowed by taking over the
	// one the caller was sitting in are restored either way: closing one would
	// take the user's own shell down with it, which is not what tearing a
	// dashboard down was asked to do.
	//
	// Off by default, which is `cmdman compose mux down` typed at a prompt: a
	// window that goes away underneath a command line is a surprise, and the
	// restored one is the answer that has always been given there.
	KillCreated bool
	// Stdout is where per-window lines and the zero-match note are written.
	// Empty defaults to os.Stdout.
	Stdout io.Writer
}

// Down tears down every cmdman-owned dashboard matching opts.Identity
// (server-wide, or limited to opts.SessionName when set). For each match it
// opens the window by ID, sends the viewer detach sequence, collapses the
// dashboard's panes to a single default pane, and unsets the tmux ownership
// option. The supervised commands keep running — only the disposable viewers
// are torn down.
//
// With opts.KillCreated the windows cmdman created are closed instead, leaving
// nothing behind rather than an emptied window; the ones it borrowed from the
// caller are restored as ever, because the shell in a borrowed window is the
// user's and closing the window would end it. Closing SIGHUPs what the panes
// were running, which costs nothing here: a mux pane runs a viewer or a frame
// component, never a supervised command.
//
// A frame shown around the dashboard is not the project's to remove: the
// window is left framed and projectless. With the ownership stamp gone the
// next `mux up` no longer recognises it and builds a fresh window beside it.
// Removing the frame is the frame verbs' own teardown, and whichever of the
// two goes last hands the window back whole.
//
// Down enumerates windows via [muxctl.Server.ListWindows], which requires no
// $TMUX context and works from any pane, from run-shell, or from outside
// tmux entirely. This is the key improvement over the old Detach: Detach
// required the caller to be attached to the same session to find the window;
// Down finds it by identity stamp regardless of the calling context.
//
// When zero windows match, Down prints a friendly note (mentioning the
// identity and, when set, the session filter) and returns nil — the same
// observable behavior as the old "nothing to detach" path.
//
// When multiple windows match (e.g. the user ran `mux up` in two sessions
// for the same project), every matching window is restored; a single joined
// error is returned if any individual teardown fails, after attempting all
// remaining matches.
//
// v1 supports the tmux driver only; any other resolved driver returns a
// "not implemented" error, mirroring [Run].
func Down(ctx context.Context, opts DownOptions) error {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	server, err := resolveServer(ctx, opts.Driver, env)
	if err != nil {
		return err
	}

	// Derive the identity when the caller did not supply one. The derivation
	// mirrors Run exactly: resolveSessionName → windowName default → identity =
	// windowName. This is the standalone-mux default; compose callers always
	// supply Identity explicitly, bypassing this path entirely.
	identity := opts.Identity
	if identity == "" {
		identity = currentIdentity(ctx, server, opts.SessionName, opts.WindowName, env)
	}

	// SessionName here is purely a narrowing filter for ListWindows —
	// when empty the scan is server-wide. We do NOT fall back to
	// resolveSessionName for the filter: that context-dependence (reading
	// $TMUX / running display-message) is the root cause this plan fixes.
	// Only the identity *derivation* above may use resolveSessionName.
	rows, err := server.ListWindows(ctx, muxctl.ListOptions{
		Session:  opts.SessionName,
		Identity: identity,
	})
	if err != nil {
		return fmt.Errorf("mux: enumerate owned windows: %w", err)
	}

	if len(rows) == 0 {
		if opts.SessionName != "" {
			fmt.Fprintf(
				stdout,
				"No cmdman dashboard found for identity %q in session %q\n",
				identity,
				opts.SessionName,
			)
		} else {
			fmt.Fprintf(
				stdout,
				"No cmdman dashboard found for identity %q\n",
				identity,
			)
		}
		return nil
	}

	var errs []error
	for _, row := range rows {
		sess, ok, openErr := server.Open(ctx, muxctl.Config{
			WindowID:         row.WindowID,
			ViewerDetachKeys: viewerDetachKeys,
		})
		if openErr != nil {
			errs = append(errs, fmt.Errorf(
				"mux: open window %s (%s in session %s): %w",
				row.WindowName, row.WindowID, row.SessionName, openErr,
			))
			continue
		}
		if !ok {
			// Window disappeared between ListWindows and Open; not
			// an error — another process or the user already tore it down.
			continue
		}
		if opts.KillCreated && row.Created {
			if closeErr := sess.Close(ctx); closeErr != nil {
				errs = append(errs, fmt.Errorf(
					"mux: close window %s (%s in session %s): %w",
					row.WindowName, row.WindowID, row.SessionName, closeErr,
				))
				continue
			}
			fmt.Fprintf(
				stdout,
				"Removed window %s (%s) in session %s\n",
				row.WindowName, row.WindowID, row.SessionName,
			)
			continue
		}
		if detachErr := sess.Detach(ctx); detachErr != nil {
			errs = append(errs, fmt.Errorf(
				"mux: detach window %s (%s in session %s): %w",
				row.WindowName, row.WindowID, row.SessionName, detachErr,
			))
			continue
		}
		fmt.Fprintf(
			stdout,
			"Restored window %s (%s) in session %s\n",
			row.WindowName, row.WindowID, row.SessionName,
		)
	}
	return errors.Join(errs...)
}

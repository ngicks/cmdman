package mux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// DownOptions configures [Down].
type DownOptions struct {
	// Driver selects the multiplexer driver. Empty autodetects from Env the
	// same way [Run] does ($TMUX > $ZELLIJ > tmux).
	Driver string
	// DriverOpt carries driver-specific options; the tmux driver honors "path"
	// and "socket". A dashboard built on a non-default socket can only be found
	// when the same socket is supplied here.
	DriverOpt map[string]string
	// SessionName, when non-empty, narrows the scan to that session only.
	// It is a pure filter passed to [tmux.ListOwnedWindows]; it does NOT
	// participate in identity derivation (only the identity defaulting path
	// uses the resolved session name). An explicit --session is therefore
	// optional for teardown: omitting it restores every matching dashboard
	// server-wide.
	SessionName string
	// WindowName is used solely for identity derivation when Identity is empty
	// (standalone-mux default). It is NOT used as a session-filter fallback.
	// Empty defaults to the resolved session name, exactly as [Run] does.
	WindowName string
	// Identity is the opaque ownership string passed to [tmux.ListOwnedWindows]
	// as the filter. When empty, it is derived the same way [Run] defaults it:
	// resolveSessionName → windowName default → identity = windowName. This
	// derivation is the documented standalone-mux limitation (a dashboard built
	// with the default naming in a different session resolves a different
	// identity). Compose callers always pass Identity explicitly, which
	// eliminates the context-dependence entirely.
	Identity string
	// Env is the process env consulted for driver autodetection. Empty defaults
	// to os.Environ().
	Env []string
	// Stdout is where per-restored-window lines and the zero-match note are
	// written. Empty defaults to os.Stdout.
	Stdout io.Writer
}

// Down tears down every cmdman-owned dashboard matching opts.Identity
// (server-wide, or limited to opts.SessionName when set). For each match it
// opens the window by ID, sends the viewer detach sequence, collapses the
// window to a single shell pane, and unsets the tmux ownership option. The
// supervised commands keep running — only the disposable viewers are torn down.
//
// Down enumerates windows via [tmux.ListOwnedWindows], which requires no
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

	driver := resolveDriver(opts.Driver, env)
	if driver != "tmux" {
		return fmt.Errorf(
			"mux: driver %q is not implemented yet (v1 ships tmux only)", driver,
		)
	}

	// Derive the identity when the caller did not supply one. The derivation
	// mirrors Run exactly: resolveSessionName → windowName default → identity =
	// windowName. This is the standalone-mux default; compose callers always
	// supply Identity explicitly, bypassing this path entirely.
	identity := opts.Identity
	if identity == "" {
		path, socket := opts.DriverOpt["path"], opts.DriverOpt["socket"]
		sessionName := resolveSessionName(
			opts.SessionName,
			env,
			func() (string, error) { return currentTmuxSession(ctx, path, socket) },
		)
		identity = deriveIdentity("", opts.WindowName, sessionName)
	}

	// SessionName here is purely a narrowing filter for ListOwnedWindows —
	// when empty the scan is server-wide. We do NOT fall back to
	// resolveSessionName for the filter: that context-dependence (reading
	// $TMUX / running display-message) is the root cause this plan fixes.
	// Only the identity *derivation* above may use resolveSessionName.
	rows, err := tmux.ListOwnedWindows(ctx, tmux.ListOwnedWindowsOptions{
		Path:     opts.DriverOpt["path"],
		Socket:   opts.DriverOpt["socket"],
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
		sess, ok, openErr := tmux.OpenExisting(ctx, tmux.Config{
			Path:             opts.DriverOpt["path"],
			Socket:           opts.DriverOpt["socket"],
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
			// Window disappeared between ListOwnedWindows and OpenExisting; not
			// an error — another process or the user already tore it down.
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

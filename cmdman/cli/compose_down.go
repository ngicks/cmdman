package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/go-common/contextkey"
)

// muxTeardown removes a project's multiplexer windows — [mux.Down] as
// [closeProjectWindows] calls it, taken as a value so the call can be observed
// without a multiplexer server to make it against.
type muxTeardown func(context.Context, mux.DownOptions) error

// CloseProjectWindows closes the project's multiplexer windows after a down
// that removed every command. It is the tail of `compose down --close-windows`;
// see closeProjectWindows for what it closes and what it spares.
//
// A window that would not close is reported on errOut as a warning, and the
// down it follows still succeeded.
func CloseProjectWindows(
	ctx context.Context,
	selection compose.ProjectSelection,
	errOut io.Writer,
) {
	closeProjectWindows(ctx, selection, mux.Down, errOut)
}

// closeProjectWindows takes the project's multiplexer windows down after a
// teardown that removed every command: their panes view commands that no longer
// exist, and the ownership stamp they carry would report the project as running
// the next time the launcher lists it.
//
// The TUI asks for it on every down, while `cmdman compose down` does it only
// under --close-windows: a window that closes underneath a command line is a
// surprise, while the TUI issued the gesture that emptied it. Either way the
// window a pane of which runs this process is restored rather than closed, as
// [mux.Down] does for every KillCreated teardown.
//
// The windows are found by the project's identity alone, which is why this goes
// to mux directly rather than through the compose mux verbs: a project with no
// mux: section has no dashboard but does have the bare shell window its landing
// synthesized, under that same identity, and the compose verbs are only for a
// project that declares the section. Only a declared section has a driver to
// name; without one the driver is left to autodetect.
//
// The per-window lines mux writes are discarded. The command line's stdout
// carries the down's progress output, JSON records included, and a line of
// plain text among them would break whatever reads them.
//
// Failing to remove a window is not the down failing: the commands are gone
// either way, and what the caller reports is about them. The failure is said in
// the log and as a warning on errOut. The TUI passes io.Discard, because its
// terminal is the screen it draws.
func closeProjectWindows(
	ctx context.Context,
	selection compose.ProjectSelection,
	muxDown muxTeardown,
	errOut io.Writer,
) {
	opts := mux.DownOptions{
		Identity:    selection.ProjectIdentity(),
		KillCreated: true,
		Stdout:      io.Discard,
	}
	if selection.Spec != nil && selection.Spec.Mux != nil {
		opts.Driver = selection.Spec.Mux.Driver
	}
	if err := muxDown(ctx, opts); err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
			ctx, "compose down: remove project window",
			"project", selection.Project, "workdir", selection.WorkDir, "error", err,
		)
		fmt.Fprintf(errOut, "warning: compose down: close project windows: %v\n", err)
	}
}

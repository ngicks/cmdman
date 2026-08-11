package mux

import (
	"context"
	"fmt"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// LandOptions configures [Land].
type LandOptions struct {
	// Driver selects and configures the multiplexer server exactly as
	// [RunOptions.Driver] does for [Run]: an empty Driver.Name autodetects from
	// Env ($TMUX > $ZELLIJ > tmux). A window built on a dedicated socket is only
	// found — and only attached — when the same Socket is supplied here.
	Driver muxctl.DriverSpec
	// SessionName, when non-empty, restricts the search to that session and is
	// the session a missing window is created in. Empty searches the whole
	// server (a project's window may well live in the session it was brought up
	// in, not the one the caller is sitting in) and creates in the caller's
	// current session, resolved as [Run] resolves it.
	SessionName string
	// WindowName names the window to create when none carries Identity. Empty
	// defaults to the resolved session name, as in [Run].
	WindowName string
	// Identity is the ownership stamp searched for, and stamped on a window this
	// call creates. Empty derives it the way [Run] and [Down] do (window name,
	// else session name).
	Identity string
	// WorkDir is the directory a created window's shell starts in — the project
	// directory, so D9's synthesized window opens where the project lives. It is
	// ignored when the window already exists.
	WorkDir string
	// Env is the process env consulted for driver autodetection and for whether
	// the caller is inside a multiplexer. Empty defaults to os.Environ().
	Env []string
}

// LandResult reports what [Land] put in front of the caller.
type LandResult struct {
	// SessionName is the session holding the landed-on window.
	SessionName string
	// WindowID is the driver-assigned id of that window (e.g. tmux "@3").
	WindowID string
	// Created reports that no window carried the identity and a bare one-pane
	// shell window was synthesized for it (D9, and D8's create half).
	Created bool
	// AttachCommand is the argv that hands the caller's terminal to the session,
	// set only when the caller is outside the multiplexer: with no client of its
	// own there is nothing to switch, so landing means becoming a client (D8).
	// Nil when a client is already in place.
	AttachCommand []string
}

// Land puts the caller in front of a project's cmdman-owned window: it finds the
// window by ownership stamp, creates a bare shell window when the project has
// none (a project with no mux: section — D9 — or one whose dashboard was torn
// down), and focuses it.
//
// Focusing is context-sensitive by necessity (D8). Inside a multiplexer the
// window is selected and the caller's client is switched to its session, so the
// landing is complete when Land returns. Outside one the window is still
// selected — so the session shows it the moment anybody looks — and
// AttachCommand carries the argv the caller must run to hand its terminal over,
// which cmdman cannot do for it: the terminal belongs to the calling process.
//
// Land is deliberately not part of [Run]: bringing a dashboard up and going
// there are separate gestures (D4's `s` versus `S`), and re-applying a layout
// just to move focus would tear down the panes of a project that is already up.
func Land(ctx context.Context, opts LandOptions) (LandResult, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}

	server, err := resolveServer(ctx, opts.Driver, env)
	if err != nil {
		return LandResult{}, err
	}

	// Two different questions: where the caller is sitting (which decides whose
	// client moves) and which session a missing window is created in. They are
	// the same session unless the caller named one, and an explicit session
	// never means "and move that session's client instead of mine".
	callerSession := resolveSessionName(
		"",
		env,
		func() (string, bool, error) { return server.CurrentSessionName(ctx) },
	)
	sessionName := opts.SessionName
	if sessionName == "" {
		sessionName = callerSession
	}
	windowName := opts.WindowName
	if windowName == "" {
		windowName = sessionName
	}
	identity := deriveIdentity(opts.Identity, opts.WindowName, sessionName)

	rows, err := server.ListWindows(ctx, muxctl.ListOptions{
		Session:  opts.SessionName,
		Identity: identity,
	})
	if err != nil {
		return LandResult{}, fmt.Errorf("mux: enumerate owned windows: %w", err)
	}

	var res LandResult
	if row, ok := pickLandingWindow(rows, sessionName); ok {
		res.SessionName, res.WindowID = row.SessionName, row.WindowID
	} else {
		sess, err := server.New(ctx, muxctl.Config{
			SessionName:   sessionName,
			WindowName:    windowName,
			OwnedIdentity: identity,
			// Never take over the window the caller is standing in: landing means
			// going to the project's own window, and a popup's notion of "current
			// window" is whatever the user was looking at.
			ReuseCurrentWindow: false,
			StartDirectory:     opts.WorkDir,
		})
		if err != nil {
			return LandResult{}, fmt.Errorf(
				"mux: create window %q in session %q: %w", windowName, sessionName, err)
		}
		res.SessionName, res.WindowID, res.Created = sessionName, sess.WindowID(), true
	}

	inside := envOf(env, "TMUX") != "" || envOf(env, "ZELLIJ") != ""
	focus := muxctl.FocusOptions{SwitchClient: inside}
	if inside {
		// The caller's own session is how the driver picks which client to move;
		// see muxctl.FocusOptions.ClientSession for why guessing is not an option.
		focus.ClientSession = callerSession
	}
	if err := server.FocusWindow(ctx, res.WindowID, focus); err != nil {
		return res, fmt.Errorf("mux: focus window %s: %w", res.WindowID, err)
	}
	if !inside {
		res.AttachCommand = server.AttachCommand(res.SessionName)
	}
	return res, nil
}

// pickLandingWindow chooses which of the identity's windows to land on. A window
// in the caller's own session wins: the same project brought up in two sessions
// should take the caller to the one next door, not drag their client elsewhere.
func pickLandingWindow(rows []muxctl.Window, sessionName string) (muxctl.Window, bool) {
	if len(rows) == 0 {
		return muxctl.Window{}, false
	}
	for _, row := range rows {
		if row.SessionName == sessionName {
			return row, true
		}
	}
	return rows[0], true
}

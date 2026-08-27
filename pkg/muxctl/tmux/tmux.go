package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// ownerOption is the window-level tmux user option that records the
// cmdman-assigned identity for this window. It is set by [New] after the
// window is resolved and cleared by [Session.Detach] when the window is
// restored. A non-empty value is the authoritative ownership signal: ownership
// recognition checks this option rather than requiring every pane to carry
// @cmdman_marker, so the check survives manual pane splits and pane churn
// across layout re-applies.
const ownerOption = "@cmdman_window"

// createdOption is the window-level tmux user option marking a window cmdman
// created itself, as opposed to one it borrowed by taking over the window the
// caller was sitting in. Only a created window may be killed on teardown:
// kill-window SIGHUPs everything in the window, and a borrowed one holds the
// user's own shell — cmdman's panes hold viewers it can afford to lose, that
// shell holds work it cannot.
//
// It records where the window came from, not whether cmdman is still in it, so
// it outlives a per-side teardown and is cleared only when the window is handed
// back whole (see [Session.releaseWindowIfLast]).
const createdOption = userOptionPrefix + "created"

// createdStamp is the value written into createdOption; only its
// non-emptiness is meaningful to readers.
const createdStamp = "1"

// Server is a tmux server bound once by [Driver.Connect]. It builds, enumerates,
// and stores per-window state for cmdman-owned windows through a single shared
// executor — the tmux binary and -L socket captured at Connect time — so its
// session-less operations no longer thread that addressing per call. It
// implements [muxctl.Server]; the sessions [Server.New] and [Server.Open] return
// run their commands through the same executor.
type Server struct {
	exec *executor
}

var _ muxctl.Server = (*Server)(nil)

// Session is a tmux-backed [muxctl.Session] controlling one window. It is built
// by [Server.New] or [Server.Open] and runs its tmux commands through the
// executor shared with the parent [Server]; the retained [muxctl.Config] supplies
// the driver-agnostic window settings (e.g. ViewerDetachKeys).
type Session struct {
	cfg      muxctl.Config
	exec     *executor
	windowID string
}

var _ muxctl.Session = (*Session)(nil)

// CurrentSessionName implements [muxctl.Server.CurrentSessionName]. It runs
// "display-message -p '#{session_name}'" through the shared executor, which
// resolves the session of the attached client. Any failure (no server, no
// attached client, tmux error) means "not inside the multiplexer": it yields
// ok=false with a nil error rather than surfacing the tmux diagnostic, mirroring
// the tolerance the mux layer applied when it shelled out to tmux itself.
func (srv *Server) CurrentSessionName(ctx context.Context) (name string, ok bool, err error) {
	out, runErr := srv.exec.run(ctx, "display-message", "-p", "#{session_name}")
	if runErr != nil || out == "" {
		return "", false, nil
	}
	return out, true, nil
}

// CurrentWindowID implements [muxctl.Server.CurrentWindowID] as
// "display-message -p '#{window_id}'", resolved against the caller's attached
// client or, when session is named, against that session's current window.
//
// The target is the "=<session>:" form: the trailing colon makes it a window
// target so tmux answers with the session's current window, and the leading =
// keeps the session match exact, as everywhere else in this driver. Without the
// colon tmux reads "=<session>" as a pane target and resolves nothing (measured
// on a scratch server: empty output, exit 0).
//
// Any failure — no server, no such session, no attached client — is ok=false
// with a nil error, mirroring [Server.CurrentSessionName]: both answer "where is
// the caller", and having no answer is not an error.
func (srv *Server) CurrentWindowID(
	ctx context.Context,
	session string,
) (id string, ok bool, err error) {
	args := []string{"display-message", "-p"}
	if session != "" {
		args = append(args, "-t", "="+session+":")
	}
	args = append(args, "#{window_id}")

	out, runErr := srv.exec.run(ctx, args...)
	if runErr != nil || out == "" {
		return "", false, nil
	}
	return out, true, nil
}

// New constructs a Session targeting either a known window (cfg.WindowID) or a
// freshly created one named cfg.WindowName in cfg.SessionName. It does not apply
// any layout — call [Session.ApplyLayout] to populate the window.
//
// Without a cfg.WindowID New always CREATES: an existing window of the same name
// is never adopted, because a window name is display-only (the user renames it,
// a program sets it via OSC 2, tmux automatic-rename rewrites it) and two
// projects may well want the same one. Finding the window a caller already owns
// is the caller's job, through [Server.ListWindows] filtered by identity, and it
// hands the result back as cfg.WindowID.
//
// With cfg.ReuseCurrentWindow the caller's current window is taken over instead
// when it is this caller's own (its @cmdman_window slot holds
// cfg.OwnedIdentity), when it holds a frame but no project, or when it is
// unowned and safe to repurpose — see [currentWindowToReuse]. Another project's
// window is never taken over: its region would be rebuilt out from under it.
func (srv *Server) New(ctx context.Context, cfg muxctl.Config) (muxctl.Session, error) {
	e := srv.exec

	var wid string
	switch {
	case cfg.WindowID != "":
		wid = cfg.WindowID
	default:
		if cfg.SessionName == "" {
			return nil, errors.New("tmux: Config.SessionName is required when WindowID is empty")
		}
		if cfg.WindowName == "" {
			return nil, errors.New("tmux: Config.WindowName is required when WindowID is empty")
		}
		if cfg.ReuseCurrentWindow {
			if cur, ok := currentWindowToReuse(
				ctx, e, cfg.WindowName, cfg.OwnedIdentity,
			); ok {
				wid = cur
			}
		}
		if wid == "" {
			if err := ensureSession(ctx, e, cfg.SessionName, cfg.StartDirectory); err != nil {
				return nil, fmt.Errorf("tmux: ensure session %q: %w", cfg.SessionName, err)
			}
			created, err := createWindow(
				ctx, e, cfg.SessionName, cfg.WindowName, cfg.StartDirectory,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"tmux: create window %q in session %q: %w",
					cfg.WindowName, cfg.SessionName, err,
				)
			}
			wid = created

			// Record that this window is cmdman's own, so a teardown that
			// wants the window gone may kill it. Only this branch stamps:
			// the two paths above address a window the caller handed over,
			// and killing one of those takes the shell it was running down
			// with it.
			if _, err := e.run(
				ctx, "set-option", "-w", "-t", wid, createdOption, createdStamp,
			); err != nil {
				return nil, fmt.Errorf("tmux: stamp %s: %w", createdOption, err)
			}
		}
	}

	// Enable the pane-border title row so per-pane titles are visible.
	if _, err := e.run(
		ctx,
		"set-option", "-w", "-t", wid, "pane-border-status", "top",
	); err != nil {
		return nil, fmt.Errorf("tmux: enable pane-border-status: %w", err)
	}

	// Stamp the ownership identity onto the window so it can be enumerated
	// and recognised later without relying on every pane carrying a marker
	// (which breaks when the user manually splits a pane). Skip when the
	// caller did not supply an identity — callers that do not need
	// enumeration (one-off builds, tests) simply leave this unset.
	if cfg.OwnedIdentity != "" {
		if _, err := e.run(
			ctx,
			"set-option", "-w", "-t", wid, ownerOption, cfg.OwnedIdentity,
		); err != nil {
			return nil, fmt.Errorf("tmux: stamp %s: %w", ownerOption, err)
		}
	}

	return &Session{cfg: cfg, exec: e, windowID: wid}, nil
}

// Open locates an already-existing cmdman-owned window. It NEVER creates a
// session or a window, and NEVER mutates any window option (in particular it
// does not enable pane-border-status the way [New] does). It is the entry point
// for teardown operations such as [Session.Detach] that must act only on a
// window cmdman already built — never spawn a stray one.
//
// ok is false (with a nil error and nil Session) when no such window is found,
// so callers can no-op instead of creating one. Resolution:
//
//   - cfg.WindowID != "" targets that window directly.
//   - otherwise, when cfg.ReuseCurrentWindow is set and the caller's current
//     window carries cfg.OwnedIdentity in the @cmdman_window option (i.e. a
//     previous [New] call stamped it for this same caller), that window is
//     used. Unlike [New], nothing else is accepted — not an unowned window, not
//     another project's, not a window holding only a frame: teardown must act
//     only on a window that is provably the caller's, never repurpose one the
//     user happens to be sitting in.
//   - otherwise the window named cfg.WindowName in cfg.SessionName is looked up
//     (find-only). A missing session or window yields ok=false rather than an
//     error: from a teardown caller's view, "no session/window" simply means
//     "nothing to detach".
func (srv *Server) Open(ctx context.Context, cfg muxctl.Config) (muxctl.Session, bool, error) {
	e := srv.exec

	var wid string
	switch {
	case cfg.WindowID != "":
		wid = cfg.WindowID
	default:
		if cfg.ReuseCurrentWindow {
			if cur, ok := currentWindowIfOwned(ctx, e, cfg.OwnedIdentity); ok {
				wid = cur
			}
		}
		if wid == "" {
			if cfg.SessionName == "" || cfg.WindowName == "" {
				return nil, false, nil
			}
			// A missing session means there is nothing to detach: treat it as a
			// clean no-op rather than surfacing tmux's "can't find session".
			if _, err := e.run(ctx, "has-session", "-t", "="+cfg.SessionName); err != nil {
				return nil, false, nil
			}
			found, ok, err := findWindow(ctx, e, cfg.SessionName, cfg.WindowName)
			if err != nil {
				return nil, false, fmt.Errorf(
					"tmux: find window %q in session %q: %w",
					cfg.WindowName, cfg.SessionName, err,
				)
			}
			if !ok {
				return nil, false, nil
			}
			wid = found
		}
	}

	return &Session{cfg: cfg, exec: e, windowID: wid}, true, nil
}

// WindowID returns the tmux @id of the cmdman-owned window. Useful for
// callers (and tests) that want to query the window outside the driver.
func (s *Session) WindowID() string {
	return s.windowID
}

// Close kills the cmdman-owned window. It MUST NOT affect any process the
// panes were observing — but tmux's kill-window will SIGHUP the in-pane
// processes, which is fine for cmdman because mux panes only run viewer
// processes (attach / logs), not the supervised commands themselves.
func (s *Session) Close(ctx context.Context) error {
	_, err := s.exec.run(ctx, "kill-window", "-t", s.windowID)
	if err != nil {
		return fmt.Errorf("tmux: close window %s: %w", s.windowID, err)
	}
	return nil
}

// ensureSession creates the named session detached if it does not exist,
// starting its first shell in startDir when that is non-empty. A "duplicate
// session" race (the session appeared between has-session and new-session) is
// treated as success.
func ensureSession(ctx context.Context, e *executor, name, startDir string) error {
	if _, err := e.run(ctx, "has-session", "-t", "="+name); err == nil {
		return nil
	}
	args := []string{"new-session", "-d", "-s", name}
	if startDir != "" {
		args = append(args, "-c", startDir)
	}
	if _, err := e.run(ctx, args...); err != nil {
		if strings.Contains(err.Error(), "duplicate session") {
			return nil
		}
		return err
	}
	return nil
}

// findWindow looks up a window by exact name within the session and returns
// its @id. ok is false (with a nil error) when no such window exists. It never
// creates a window.
//
// A name is a weak key — it is not unique within a session and the user, the
// in-pane program, or tmux itself may change it — so this is the last resort
// [Server.Open] falls back to when it has no window id and no current window to
// go by. Nothing that builds a window resolves one this way.
func findWindow(
	ctx context.Context,
	e *executor,
	sessionName, windowName string,
) (string, bool, error) {
	out, err := e.run(
		ctx,
		"list-windows", "-t", sessionName,
		"-F", "#{window_id}\t#{window_name}",
	)
	if err != nil {
		return "", false, err
	}
	for line := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && parts[1] == windowName {
			return parts[0], true, nil
		}
	}
	return "", false, nil
}

// createWindow appends a window named windowName to the session (detached, with
// a default shell pane started in startDir when that is non-empty) and returns
// its @id. An existing window of the same name is left alone: tmux allows
// duplicate window names, and the name says what the window displays, never
// which window this is.
//
// The target is "-a -t =<session>:{end}" — insert after the session's last
// window — rather than the session alone. tmux resolves a bare "-t <session>"
// against window NAMES first, so a session holding a window named like it (the
// standalone dashboard: window "cmdman" in session "cmdman") or like a prefix of
// it resolves to that window's index and the create fails with "index N in use".
// Naming the insertion point leaves no name for tmux to match.
func createWindow(
	ctx context.Context,
	e *executor,
	sessionName, windowName, startDir string,
) (string, error) {
	args := []string{
		"new-window", "-d", "-a", "-t", "=" + sessionName + ":{end}",
		"-n", windowName,
		"-P", "-F", "#{window_id}",
	}
	if startDir != "" {
		args = append(args, "-c", startDir)
	}
	out, err := e.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

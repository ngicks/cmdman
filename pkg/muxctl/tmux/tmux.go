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

// Config configures a tmux-backed [muxctl.Session].
type Config struct {
	// Path overrides the tmux binary path. Defaults to "tmux".
	Path string

	// Socket selects a tmux server by socket name (-L). Empty omits -L, so
	// tmux uses the default socket — or, when called from inside a tmux
	// client, the current server (via $TMUX in the calling process env).
	// A non-empty Socket selects a dedicated server (the opt-in isolation
	// mode).
	Socket string

	// SessionName names the tmux session this driver targets. Required
	// unless WindowID is set (the window already identifies its session).
	// When required, the session is created detached if it does not already
	// exist; it is never killed by this driver.
	SessionName string

	// WindowName names the cmdman-owned window within SessionName.
	// Required when WindowID is empty (find-or-create by name); ignored
	// when WindowID is set. The driver operates exclusively on the
	// resolved window; sibling windows are left untouched.
	WindowName string

	// WindowID, when non-empty, targets the existing tmux window with this
	// id (e.g. "@7") and skips find-or-create by WindowName. Used by
	// callers that pick the target window themselves — e.g. "apply to the
	// caller's current window" or "create a fresh window per invocation
	// and target it." When WindowID is set, SessionName/WindowName are not
	// consulted.
	WindowID string

	// OwnedIdentity, when non-empty, is stamped onto the resolved window as
	// the window-level tmux user option @cmdman_window. It is the durable
	// ownership signal that survives pane churn, manual pane splits, and window
	// renames. Callers supply an opaque string (e.g. a workdir-hash+project
	// prefix for compose, or the window name for standalone mux); the driver
	// stores and returns it verbatim, never interpreting it. Empty disables
	// stamping — useful for one-off callers that do not need enumeration.
	OwnedIdentity string

	// ReuseCurrentWindow, when true and WindowID is empty, applies the layout
	// to the caller's current tmux window instead of a window found-or-created
	// by name — but only when that current window is safe to take over (it is
	// already muxctl-owned via @cmdman_window, already named WindowName, or
	// has a single pane). When the current window cannot be resolved or is not
	// safe to reuse, New falls back to find-or-create by name. Callers set
	// this when running inside a tmux client and they have not pinned an
	// explicit session.
	ReuseCurrentWindow bool

	// ViewerDetachKeys is the tmux send-keys key sequence (e.g.
	// {"C-p", "C-q"}) that ApplyLayout sends to the in-pane viewers of a
	// previous build to make them detach gracefully before the window is torn
	// down and rebuilt. It MUST match the detach sequence those viewers
	// actually honor, expressed in tmux send-keys syntax.
	//
	// The driver does not assume any particular sequence: it differs per caller
	// (the viewers a caller spawns, and the --detach-keys they were given) and
	// per future driver, so the caller owns it. Empty disables graceful
	// detach — ApplyLayout tears the old panes down with respawn-pane -k
	// directly, which SIGKILLs the in-pane processes mid-frame (see
	// quiesceViewers for why that is undesirable for cmdman viewers).
	ViewerDetachKeys []string
}

// Session is a tmux-backed [muxctl.Session] controlling one window.
type Session struct {
	cfg      Config
	exec     *executor
	windowID string
}

var _ muxctl.Session = (*Session)(nil)

// New constructs a Session targeting either a known window (cfg.WindowID)
// or a window found-or-created by name (cfg.SessionName + cfg.WindowName).
// It does not apply any layout — call [Session.ApplyLayout] to populate
// the window.
func New(ctx context.Context, cfg Config) (*Session, error) {
	e := newExecutor(cfg.Path, cfg.Socket)

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
			if cur, ok := currentWindowToReuse(ctx, e, cfg.WindowName); ok {
				wid = cur
			}
		}
		if wid == "" {
			if err := ensureSession(ctx, e, cfg.SessionName); err != nil {
				return nil, fmt.Errorf("tmux: ensure session %q: %w", cfg.SessionName, err)
			}
			found, err := findOrCreateWindow(ctx, e, cfg.SessionName, cfg.WindowName)
			if err != nil {
				return nil, fmt.Errorf(
					"tmux: find-or-create window %q in session %q: %w",
					cfg.WindowName, cfg.SessionName, err,
				)
			}
			wid = found
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

// OpenExisting locates an already-existing cmdman-owned window WITHOUT creating
// one and WITHOUT mutating any window option (in particular it does not enable
// pane-border-status the way [New] does). It is the entry point for teardown
// operations such as [Session.Detach] that must act only on a window cmdman
// already built — never spawn a stray one.
//
// ok is false (with a nil error and nil Session) when no such window is found,
// so callers can no-op instead of creating one. Resolution:
//
//   - cfg.WindowID != "" targets that window directly.
//   - otherwise, when cfg.ReuseCurrentWindow is set and the caller's current
//     window carries the @cmdman_window option (i.e. it was stamped by a
//     previous [New] call), that window is used. Unlike [New], an unowned
//     current window is NOT taken over: detach must act only on a provably
//     muxctl-owned window, never repurpose an arbitrary window the user happens
//     to be sitting in.
//   - otherwise the window named cfg.WindowName in cfg.SessionName is looked up
//     (find-only). A missing session or window yields ok=false rather than an
//     error: from a teardown caller's view, "no session/window" simply means
//     "nothing to detach".
func OpenExisting(ctx context.Context, cfg Config) (*Session, bool, error) {
	e := newExecutor(cfg.Path, cfg.Socket)

	var wid string
	switch {
	case cfg.WindowID != "":
		wid = cfg.WindowID
	default:
		if cfg.ReuseCurrentWindow {
			if cur, ok := currentWindowIfOwned(ctx, e); ok {
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

// ensureSession creates the named session detached if it does not exist.
// A "duplicate session" race (the session appeared between has-session and
// new-session) is treated as success.
func ensureSession(ctx context.Context, e *executor, name string) error {
	if _, err := e.run(ctx, "has-session", "-t", "="+name); err == nil {
		return nil
	}
	if _, err := e.run(ctx, "new-session", "-d", "-s", name); err != nil {
		if strings.Contains(err.Error(), "duplicate session") {
			return nil
		}
		return err
	}
	return nil
}

// findWindow looks up a window by exact name within the session and returns
// its @id. ok is false (with a nil error) when no such window exists. It never
// creates a window — see findOrCreateWindow for the create-on-miss variant.
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

// findOrCreateWindow looks up a window by exact name within the session
// and returns its @id. If no such window exists, one is created
// (detached, with a default shell pane) and its @id is returned.
func findOrCreateWindow(
	ctx context.Context,
	e *executor,
	sessionName, windowName string,
) (string, error) {
	if id, ok, err := findWindow(ctx, e, sessionName, windowName); err != nil {
		return "", err
	} else if ok {
		return id, nil
	}
	out, err := e.run(
		ctx,
		"new-window", "-d", "-t", sessionName,
		"-n", windowName,
		"-P", "-F", "#{window_id}",
	)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

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

	// Enable the pane-border title row so per-pane titles are visible.
	if _, err := e.run(
		ctx,
		"set-option", "-w", "-t", wid, "pane-border-status", "top",
	); err != nil {
		return nil, fmt.Errorf("tmux: enable pane-border-status: %w", err)
	}
	return &Session{cfg: cfg, exec: e, windowID: wid}, nil
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

// findOrCreateWindow looks up a window by exact name within the session
// and returns its @id. If no such window exists, one is created
// (detached, with a default shell pane) and its @id is returned.
func findOrCreateWindow(
	ctx context.Context,
	e *executor,
	sessionName, windowName string,
) (string, error) {
	out, err := e.run(
		ctx,
		"list-windows", "-t", sessionName,
		"-F", "#{window_id}\t#{window_name}",
	)
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && parts[1] == windowName {
			return parts[0], nil
		}
	}
	out, err = e.run(
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

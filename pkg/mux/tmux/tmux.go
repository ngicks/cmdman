// Package tmux implements mux interfaces using the tmux command-line tool.
package tmux

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/ngicks/cmdman/pkg/mux"
)

// Config configures a tmux session.
type Config struct {
	// Name is the tmux session name.
	Name string
	// TmuxPath is the path to the tmux binary. Defaults to "tmux".
	TmuxPath string
	// SocketName is the tmux socket name (-L flag). Empty uses the default socket.
	SocketName string
	// StartupKeys are sent to every new pane created in this session.
	// These are sent before any window-level startup keys.
	// Supported placeholders: #{SESSION_ID}, #{WINDOW_ID}, #{PANE_ID}, #{INJECT_META}.
	// Use ##{...} to produce a literal #{...}.
	StartupKeys []string
	// DisallowReuse controls behavior when a session with the same name exists.
	// If false (default), New attaches to the existing session instead of failing.
	// If true, New returns mux.ErrSessionExists.
	DisallowReuse bool
}

// Session is a tmux session implementing mux.Session.
type Session struct {
	id   string
	name string
	exec *executor

	mu          sync.RWMutex
	startupKeys []string            // session-level, defensively copied from Config
	windowKeys     map[string][]string // window ID → window-level startup keys
}

// Verify interface compliance.
var _ mux.Session = (*Session)(nil)

// New creates a new detached tmux session with the given config.
// If Config.StartupKeys is non-empty, the keys are sent to the initial pane.
func New(ctx context.Context, cfg Config) (*Session, error) {
	exec := newExecutor(cfg.TmuxPath, cfg.SocketName)

	out, err := exec.run(ctx, "new-session", "-d", "-s", cfg.Name, "-P", "-F", "#{session_id}\t#{window_id}\t#{pane_id}")
	if err != nil {
		if strings.Contains(err.Error(), "duplicate session") {
			if cfg.DisallowReuse {
				return nil, mux.ErrSessionExists
			}
			return Attach(ctx, cfg)
		}
		return nil, err
	}

	parts := strings.SplitN(strings.TrimSpace(out), "\t", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("tmux: unexpected new-session output: %q", out)
	}
	sessionID, initialWindowID, initialPaneID := parts[0], parts[1], parts[2]

	sess := &Session{
		id:          sessionID,
		name:        cfg.Name,
		exec:        exec,
		startupKeys: slices.Clone(cfg.StartupKeys),
		windowKeys:  make(map[string][]string),
	}

	// Install hooks to rebalance all window panes on client attach/detach.
	// Splits done on detached sessions use default-size (80x24) and get
	// distorted when the window resizes to the real terminal dimensions.
	// Using run-shell to iterate all windows so every window is rebalanced.
	// Array index [100] avoids clobbering user-defined hooks.
	//
	// NOTE: ## escapes # for tmux format expansion in run-shell.
	// All dynamic values are shell-quoted with shellQuote() to prevent injection.
	rebalanceCmd := fmt.Sprintf(
		"run-shell 'for w in $(%s %slist-windows -t %s -F \"##{window_id}\"); do %s %sselect-layout -t \"$w\" tiled; done'",
		shellQuote(exec.tmuxPath), exec.socketFlag(),
		shellQuote(cfg.Name),
		shellQuote(exec.tmuxPath), exec.socketFlag(),
	)
	for _, hook := range []string{"client-attached[100]", "client-detached[100]"} {
		_, err = exec.run(ctx, "set-hook", "-t", cfg.Name, hook, rebalanceCmd)
		if err != nil {
			return nil, err
		}
	}

	// Send session-level startup keys to the initial pane.
	if err := sendStartupKeys(ctx, exec, initialPaneID, sessionID, initialWindowID, sess.startupKeys, nil); err != nil {
		return nil, err
	}

	return sess, nil
}

// Attach attaches to an existing tmux session.
func Attach(ctx context.Context, cfg Config) (*Session, error) {
	exec := newExecutor(cfg.TmuxPath, cfg.SocketName)

	_, err := exec.run(ctx, "has-session", "-t", cfg.Name)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") ||
			strings.Contains(err.Error(), "can't find session") ||
			strings.Contains(err.Error(), "session not found") {
			return nil, mux.ErrSessionNotFound
		}
		return nil, err
	}

	out, err := exec.run(ctx, "display-message", "-t", cfg.Name, "-p", "#{session_id}")
	if err != nil {
		return nil, err
	}

	return &Session{
		id:          strings.TrimSpace(out),
		name:        cfg.Name,
		exec:        exec,
		startupKeys: slices.Clone(cfg.StartupKeys),
		windowKeys:  make(map[string][]string),
	}, nil
}

func (s *Session) Id() string {
	return s.id
}

func (s *Session) Name(ctx context.Context) (string, error) {
	out, err := s.exec.run(ctx, "display-message", "-t", s.name, "-p", "#{session_name}")
	if err != nil {
		return "", err
	}
	return out, nil
}

// StartupKeys returns session-level keys sent to every new pane before window-level keys.
func (s *Session) StartupKeys() []string {
	return slices.Clone(s.startupKeys)
}

// NewWindow creates a new window and sends session-level + window-level
// startup keys to its initial pane. Window-level keys are persisted so that
// panes created later via Split also receive them. Pass nil for no window-level keys.
func (s *Session) NewWindow(ctx context.Context, name string, startupKeys []string) (mux.Window, error) {
	out, err := s.exec.run(ctx, "new-window", "-t", s.name, "-n", name, "-P", "-F", "#{window_id}\t#{pane_id}")
	if err != nil {
		return nil, err
	}

	parts := strings.SplitN(strings.TrimSpace(out), "\t", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("tmux: unexpected new-window output: %q", out)
	}
	windowID, paneID := parts[0], parts[1]

	// Store window-level keys under lock.
	s.mu.Lock()
	if len(startupKeys) > 0 {
		s.windowKeys[windowID] = slices.Clone(startupKeys)
	}
	s.mu.Unlock()

	w := &window{
		id:                 windowID,
		sessionID:          s.id,
		sessionName:        s.name,
		exec:               s.exec,
		sessionStartupKeys: s.startupKeys,
		startupKeys:        slices.Clone(startupKeys),
	}

	// Send session + window startup keys to the initial pane.
	if err := sendStartupKeys(ctx, s.exec, paneID, s.id, windowID, s.startupKeys, startupKeys); err != nil {
		return nil, err
	}

	return w, nil
}

func (s *Session) List(ctx context.Context) ([]mux.Window, error) {
	out, err := s.exec.run(ctx, "list-windows", "-t", s.name, "-F", "#{window_id}\t#{window_index}\t#{window_name}")
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return parseWindows(out, s.name, s.exec, s.startupKeys, s.windowKeys, s.id), nil
}

func (s *Session) GetAt(ctx context.Context, i int) (mux.Window, error) {
	windows, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	if i < 0 || i >= len(windows) {
		return nil, mux.ErrWindowNotFound
	}
	return windows[i], nil
}

func (s *Session) GetById(ctx context.Context, id string) (mux.Window, error) {
	windows, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range windows {
		if w.Id() == id {
			return w, nil
		}
	}
	return nil, mux.ErrWindowNotFound
}

func (s *Session) Close(ctx context.Context) error {
	_, err := s.exec.run(ctx, "kill-session", "-t", s.name)
	return err
}

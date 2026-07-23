package tmux

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Detach restores the controlled window to one shell pane and clears driver
// state without stopping observed processes.
func (s *Session) Detach(ctx context.Context) error {
	restore := s.quiesceViewers(ctx)
	defer restore()

	anchorID, err := s.resetWindow(ctx)
	if err != nil {
		return fmt.Errorf("tmux: reset window: %w", err)
	}

	_, _ = s.exec.run(ctx, "set-option", "-p", "-u", "-t", anchorID, markerOption)
	_, _ = s.exec.run(ctx, "set-option", "-p", "-u", "-t", anchorID, leafOption)

	// Respawn the anchor with a fresh shell. An explicit argv is required:
	// respawn-pane with no command re-runs the pane's previous command — here
	// the viewer we are tearing down. respawn-pane -k revives the anchor even
	// when its viewer already exited under remain-on-exit.
	if err := s.respawnPane(ctx, anchorID, []string{s.defaultShell(ctx)}); err != nil {
		return fmt.Errorf("tmux: respawn shell in anchor pane %s: %w", anchorID, err)
	}

	_, _ = s.exec.run(ctx, "select-pane", "-t", anchorID, "-T", "")

	// Unset the window-level pane-border-status that New enabled, reverting it
	// to the inherited (global) default.
	if _, err := s.exec.run(
		ctx, "set-option", "-w", "-u", "-t", s.windowID, "pane-border-status",
	); err != nil {
		return fmt.Errorf("tmux: unset pane-border-status: %w", err)
	}

	_, _ = s.exec.run(ctx, "set-option", "-w", "-u", "-t", s.windowID, ownerOption)

	_, _ = s.exec.run(ctx, "set-option", "-w", "-u", "-t", s.windowID, scaleOption)

	return nil
}

// defaultShell returns the shell tmux would spawn for a fresh pane: the
// server's default-shell option, falling back to /bin/sh. The driver stays
// env-pure (it queries tmux rather than reading $SHELL), so the restored pane
// matches what a plain tmux new-window would give.
func (s *Session) defaultShell(ctx context.Context) string {
	out, err := s.exec.run(ctx, "show-options", "-gv", "default-shell")
	if err == nil {
		if sh := strings.TrimSpace(out); sh != "" {
			return sh
		}
	}
	return "/bin/sh"
}

// quiesceDeadline bounds how long quiesceViewers waits for detached viewers to
// exit before giving up and letting ApplyLayout tear the panes down anyway.
const quiesceDeadline = 750 * time.Millisecond

// quiesceViewers gives marked viewers a bounded chance to restore their
// terminals before pane teardown. The returned restore func must be deferred.
func (s *Session) quiesceViewers(ctx context.Context) func() {
	noop := func() {}
	if len(s.cfg.ViewerDetachKeys) == 0 {
		return noop
	}
	panes, err := s.listViewerPanes(ctx)
	if err != nil || len(panes) == 0 {
		return noop
	}
	if _, err := s.exec.run(
		ctx, "set-option", "-w", "-t", s.windowID, "remain-on-exit", "on",
	); err != nil {
		return noop
	}
	for _, id := range panes {
		args := append([]string{"send-keys", "-t", id}, s.cfg.ViewerDetachKeys...)
		_, _ = s.exec.run(ctx, args...)
	}
	s.waitPanesDead(ctx, panes)
	return func() {
		_, _ = s.exec.run(
			ctx, "set-option", "-w", "-t", s.windowID, "remain-on-exit", "off",
		)
	}
}

// listViewerPanes returns the ids of the live panes in this window that carry
// a layout marker — i.e. cmdman viewers spawned by a previous ApplyLayout. The
// initial shell pane of a freshly created window has no marker and is skipped.
func (s *Session) listViewerPanes(ctx context.Context) ([]string, error) {
	out, err := s.exec.run(
		ctx, "list-panes", "-t", s.windowID,
		"-F", "#{pane_id}\t#{pane_dead}\t#{"+markerOption+"}",
	)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var ids []string
	for line := range strings.SplitSeq(out, "\n") {
		id, rest, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		dead, marker, ok := strings.Cut(rest, "\t")
		if !ok || dead == "1" || marker == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// waitPanesDead polls until every pane in ids is dead (its viewer exited) or
// quiesceDeadline elapses. Panes that are already dead or have vanished count
// as done.
func (s *Session) waitPanesDead(ctx context.Context, ids []string) {
	deadline := time.Now().Add(quiesceDeadline)
	for {
		out, err := s.exec.run(
			ctx, "list-panes", "-t", s.windowID, "-F", "#{pane_id}\t#{pane_dead}",
		)
		if err != nil {
			return
		}
		alive := make(map[string]bool)
		for line := range strings.SplitSeq(out, "\n") {
			id, dead, ok := strings.Cut(line, "\t")
			if ok && dead == "0" {
				alive[id] = true
			}
		}
		pending := false
		for _, id := range ids {
			if alive[id] {
				pending = true
				break
			}
		}
		if !pending || !time.Now().Before(deadline) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

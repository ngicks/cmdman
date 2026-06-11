package tmux

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Detach tears the cmdman-owned window down to a single clean shell pane and
// removes the driver state this Session installed, restoring the window to
// roughly the state it had before cmdman touched it: it gracefully detaches the
// in-pane viewers, collapses the window to one pane running a fresh shell, and
// unsets the per-pane @cmdman_marker option and the window's pane-border-status.
//
// Like ApplyLayout and Close, Detach MUST NOT stop any process the panes were
// observing — the viewers it tears down are disposable (the supervised commands
// live in the cmdman daemon). It is the explicit "I'm done with this dashboard,
// give me my window back" operation, distinct from Close (which kills the whole
// window, undesirable when the window was the caller's own current window).
func (s *Session) Detach(ctx context.Context) error {
	// Detach the in-pane viewers first so they restore their panes and
	// disconnect from the daemon cleanly, instead of being SIGKILLed mid-frame
	// by respawn-pane -k (see quiesceViewers). remain-on-exit keeps the panes
	// alive after their viewers exit; the deferred restore turns it back off
	// once the anchor is a clean shell again.
	restore := s.quiesceViewers(ctx)
	defer restore()

	anchorID, err := s.resetWindow(ctx)
	if err != nil {
		return fmt.Errorf("tmux: reset window: %w", err)
	}

	// Clear the layout marker on the surviving anchor (best-effort: the option
	// may already be absent on a window that never had a layout applied).
	_, _ = s.exec.run(ctx, "set-option", "-p", "-u", "-t", anchorID, markerOption)

	// Respawn the anchor with a fresh shell. An explicit argv is required:
	// respawn-pane with no command re-runs the pane's previous command — here
	// the viewer we are tearing down. respawn-pane -k revives the anchor even
	// when its viewer already exited under remain-on-exit.
	if err := s.respawnPane(ctx, anchorID, []string{s.defaultShell(ctx)}); err != nil {
		return fmt.Errorf("tmux: respawn shell in anchor pane %s: %w", anchorID, err)
	}

	// Clear the pane title ApplyLayout set (best-effort).
	_, _ = s.exec.run(ctx, "select-pane", "-t", anchorID, "-T", "")

	// Unset the window-level pane-border-status that New enabled, reverting it
	// to the inherited (global) default.
	if _, err := s.exec.run(
		ctx, "set-option", "-w", "-u", "-t", s.windowID, "pane-border-status",
	); err != nil {
		return fmt.Errorf("tmux: unset pane-border-status: %w", err)
	}

	// Clear the ownership stamp so the window is no longer enumerable as a
	// cmdman-owned window. Best-effort: the option may be absent on a session
	// that was built before identity stamping was introduced.
	_, _ = s.exec.run(ctx, "set-option", "-w", "-u", "-t", s.windowID, ownerOption)

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

// quiesceViewers gracefully detaches the cmdman viewers (attach / logs)
// running in this window's panes before ApplyLayout tears the window down and
// rebuilds it. It returns a restore func the caller MUST defer.
//
// Why this matters: respawn-pane -k replaces a pane's process by SIGKILLing
// the old one mid-frame. A cmdman attach viewer killed that way never runs its
// terminal restore (leave the alternate screen, reset SGR) and never
// disconnects from the daemon, so two clients briefly drive the command's
// geometry and the next viewer replays the command's scrollback over whatever
// the dead viewer left on the alternate screen — cells the new viewer does not
// repaint then show stale characters ("garbage in areas where redraw is not
// happening").
//
// Detaching first lets each viewer exit cleanly (restoring its pane and
// disconnecting from the daemon) so exactly one client drives the command
// across the rebuild. remain-on-exit keeps the panes — and the window — alive
// after their viewers exit so ApplyLayout can respawn into them; the returned
// restore func turns it back off.
//
// The detach key sequence is caller-provided ([Config.ViewerDetachKeys]): the
// driver does not know which keys a given caller's viewers honor. When the
// caller supplies none, there is nothing to send, so this is a no-op and the
// panes are left for the respawn-pane -k teardown.
//
// Best-effort and bounded: only panes we previously applied (carrying the
// marker option) and still live are detached, so a first run with just the
// initial shell pane is a no-op. Panes whose viewer does not exit within
// quiesceDeadline are left for the respawn-pane -k teardown.
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

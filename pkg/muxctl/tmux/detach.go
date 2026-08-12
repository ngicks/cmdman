package tmux

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Detach is the project side's teardown: it collapses the project region to a
// single default pane and clears the project's driver state, without stopping
// any observed process.
//
// A frame shown around the project is not the project's to remove: its panes,
// their stamps and the window's frame state all survive, so the window is left
// framed and empty — the same state a frame shown before anything was launched
// leaves behind, ready for the next project. [Session.HideFrame] is the other
// side's teardown.
//
// On a window carrying no frame this clears the last cmdman state, so the
// window itself is given back too (see releaseWindowIfLast): the whole-window
// restore Detach has always performed.
func (s *Session) Detach(ctx context.Context) error {
	restore := s.quiesceViewers(ctx)
	defer restore()

	// A framed window can outlive the project's panes: its viewers exit, tmux
	// reaps them, and the frame keeps the window open with the project's state
	// still stamped on it. Detach is the only call that clears that stamp, so a
	// missing region to collapse is a step to skip, not a reason to fail.
	panes, err := listPanesByRole(ctx, s.exec, s.windowID)
	if err != nil {
		return err
	}
	if len(panes.project) > 0 {
		if err := s.collapseProjectRegion(ctx); err != nil {
			return err
		}
	}

	_, _ = s.exec.run(ctx, "set-option", "-w", "-u", "-t", s.windowID, ownerOption)

	_, _ = s.exec.run(ctx, "set-option", "-w", "-u", "-t", s.windowID, scaleOption)

	return s.releaseWindowIfLast(ctx)
}

// collapseProjectRegion reduces the project's panes to one and returns that one
// to a default pane: layout stamps cleared, a fresh shell in place of the
// viewer, no border title left over. On a framed window the focus is settled
// back into that pane afterwards — collapsing kills the panes the user may have
// been sitting in, and tmux's fallback is the last-active pane, which can be a
// frame pane.
func (s *Session) collapseProjectRegion(ctx context.Context) error {
	anchorID, framed, err := s.resetWindow(ctx)
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

	if framed {
		return s.focusMainRegion(ctx, anchorID)
	}
	return nil
}

// releaseWindowIfLast completes a per-side teardown once that side has cleared
// its own state: when nothing cmdman-owned is left on the window, the window
// itself is handed back — the pane-border row [Server.New] turned on is turned
// off again, reverting it to the inherited default.
//
// While the other side is still there the window is left as it stands: a frame
// needs the border row for its own titles, and a project's state is not the
// frame's to clear. That is what keeps a per-side teardown from leaving a
// half-restored window neither side recognizes.
func (s *Session) releaseWindowIfLast(ctx context.Context) error {
	left, err := s.hasCmdmanState(ctx)
	if err != nil {
		return err
	}
	if left {
		return nil
	}
	if _, err := s.exec.run(
		ctx, "set-option", "-w", "-u", "-t", s.windowID, "pane-border-status",
	); err != nil {
		return fmt.Errorf("tmux: unset pane-border-status: %w", err)
	}
	return nil
}

// hasCmdmanState reports whether the window still carries any cmdman state: a
// window-level user option in the @cmdman_ namespace, or a pane still wearing
// one of the per-pane stamps. Each side's teardown asks after clearing its own,
// so the answer is "is the other side still here".
//
// The window scope is scanned by prefix instead of by naming the options the
// driver writes: the per-window state slots are an open vocabulary
// ([muxctl.StateKey] maps any key to @cmdman_<key>), and a scan listing them by
// name would quietly stop noticing a slot added later — a window would then be
// released while it still held state.
func (s *Session) hasCmdmanState(ctx context.Context) (bool, error) {
	out, err := s.exec.run(ctx, "show-options", "-w", "-t", s.windowID)
	if err != nil {
		return false, fmt.Errorf("tmux: list window options for %s: %w", s.windowID, err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, userOptionPrefix) {
			return true, nil
		}
	}
	// The three pane stamps are concatenated into one field: only their
	// emptiness is being asked about, and tmux drops a trailing empty field, so
	// a single field is one less shape to guess at.
	out, err = s.exec.run(
		ctx, "list-panes", "-t", s.windowID,
		"-F", "#{pane_id}\t#{"+frameOption+"}#{"+markerOption+"}#{"+leafOption+"}",
	)
	if err != nil {
		return false, fmt.Errorf("tmux: list panes for %s: %w", s.windowID, err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if _, stamps, _ := strings.Cut(line, "\t"); stamps != "" {
			return true, nil
		}
	}
	return false, nil
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
//
// Frame panes are skipped on their stamp alone, before the marker is consulted:
// the detach keys belong to the project's viewers, and a frame pane carrying a
// marker is stale state, not consent to type into a widget.
func (s *Session) listViewerPanes(ctx context.Context) ([]string, error) {
	out, err := s.exec.run(
		ctx, "list-panes", "-t", s.windowID,
		"-F", "#{pane_id}\t#{pane_dead}\t#{"+frameOption+"}\t#{"+markerOption+"}",
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
		dead, rest, ok := strings.Cut(rest, "\t")
		if !ok || dead == "1" {
			continue
		}
		// A dropped trailing field reads as an unset option, which is what
		// tmux versions that strip it mean by omitting it.
		frame, marker, _ := strings.Cut(rest, "\t")
		if frame != "" || marker == "" {
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

package tmux

import (
	"context"
	"strings"
	"time"
)

// viewerDetachKeys is the tmux key sequence sent to in-pane cmdman viewers to
// make them detach gracefully before the window is rebuilt. It mirrors
// cmdman's default --detach-keys (ctrl-p,ctrl-q); the mux family spawns
// attach/logs viewers without overriding that default, so this is the active
// sequence. A viewer that does not honor it is handled by the bounded wait
// below falling through to the respawn-pane -k teardown.
var viewerDetachKeys = []string{"C-p", "C-q"}

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
// Best-effort and bounded: only panes we previously applied (carrying the
// marker option) and still live are detached, so a first run with just the
// initial shell pane is a no-op. Panes whose viewer does not exit within
// quiesceDeadline are left for the respawn-pane -k teardown.
func (s *Session) quiesceViewers(ctx context.Context) func() {
	noop := func() {}
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
		args := append([]string{"send-keys", "-t", id}, viewerDetachKeys...)
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

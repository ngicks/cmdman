package tmux

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// quiesceSinglePane gracefully detaches the cmdman viewer (attach / logs)
// running in paneID before RespawnLeaf respawns it. It mirrors the logic of
// quiesceViewers but operates on a single targeted pane rather than the whole
// window. It returns a restore func the caller MUST defer.
//
// When cfg.ViewerDetachKeys is empty there is nothing to send, so this is a
// no-op and the pane is left for respawn-pane -k. Panes whose viewer does not
// exit within quiesceDeadline are also left for the respawn-pane -k teardown.
func (s *Session) quiesceSinglePane(ctx context.Context, paneID string) func() {
	noop := func() {}
	if len(s.cfg.ViewerDetachKeys) == 0 {
		return noop
	}
	if _, err := s.exec.run(
		ctx, "set-option", "-w", "-t", s.windowID, "remain-on-exit", "on",
	); err != nil {
		return noop
	}
	args := append([]string{"send-keys", "-t", paneID}, s.cfg.ViewerDetachKeys...)
	_, _ = s.exec.run(ctx, args...)
	s.waitPanesDead(ctx, []string{paneID})
	return func() {
		_, _ = s.exec.run(
			ctx, "set-option", "-w", "-t", s.windowID, "remain-on-exit", "off",
		)
	}
}

// stampLeaf sets the pane title, optionally the marker option, and the leaf
// option on paneID, then respawns the pane with the leaf's Cmd. It is the
// shared implementation used by both realizeLeafAt (full layout apply) and
// RespawnLeaf (targeted single-pane update for cycle-scale).
//
// When preserveMarker is true the markerOption is not touched (RespawnLeaf
// advances the replica but preserves the current layout index). When false,
// the marker int is written (>= 0) or cleared (< 0).
func (s *Session) stampLeaf(
	ctx context.Context,
	paneID string,
	leaf muxctl.PaneSpec,
	preserveMarker bool,
	marker int,
) error {
	// Set the pane title and marker option BEFORE respawning. respawn-pane -k
	// SIGHUPs the existing in-pane process tree; when the caller (e.g. the
	// muxctltester) is running inside that pane, it can die before any
	// follow-up tmux command lands. Setting them first lets them persist
	// regardless — tmux per-pane state survives respawn-pane.
	title := cmp.Or(leaf.CmdOpt["title"], leaf.Name)
	if _, err := s.exec.run(
		ctx, "select-pane", "-t", paneID, "-T", title,
	); err != nil {
		return fmt.Errorf("tmux: set pane title for %s: %w", paneID, err)
	}
	if !preserveMarker {
		if marker >= 0 {
			if _, err := s.exec.run(
				ctx, "set-option", "-p", "-t", paneID,
				markerOption, strconv.Itoa(marker),
			); err != nil {
				return fmt.Errorf("tmux: set marker option for %s: %w", paneID, err)
			}
		} else {
			// Clear any stale marker so a marker-less apply leaves the pane in a
			// clean state (best-effort: the option may not exist yet).
			_, _ = s.exec.run(
				ctx, "set-option", "-p", "-u", "-t", paneID, markerOption,
			)
		}
	}
	if leaf.CycleKey != "" {
		if _, err := s.exec.run(
			ctx, "set-option", "-p", "-t", paneID,
			leafOption, leaf.CycleKey,
		); err != nil {
			return fmt.Errorf("tmux: set leaf option for %s: %w", paneID, err)
		}
	} else {
		// Clear any stale leaf key from a previous apply that had CycleKey set.
		_, _ = s.exec.run(
			ctx, "set-option", "-p", "-u", "-t", paneID, leafOption,
		)
	}
	if err := s.respawnPane(ctx, paneID, leaf.Cmd); err != nil {
		return fmt.Errorf("tmux: respawn pane %s with %v: %w", paneID, leaf.Cmd, err)
	}
	return nil
}

// FindLeafPane finds the pane in windowID whose @cmdman_leaf option equals
// cycleKey. It uses list-panes -F '#{pane_id}\t#{@cmdman_leaf}' and returns
// the first matching pane id. ok is false when no pane carries the key.
func FindLeafPane(
	ctx context.Context,
	opts ListOwnedWindowsOptions,
	windowID, cycleKey string,
) (paneID string, ok bool, err error) {
	e := newExecutor(opts.Path, opts.Socket)
	out, runErr := e.run(
		ctx, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{"+leafOption+"}",
	)
	if runErr != nil {
		return "", false, fmt.Errorf("tmux: list panes for %s: %w", windowID, runErr)
	}
	for line := range strings.SplitSeq(out, "\n") {
		id, key, cut := strings.Cut(line, "\t")
		if !cut {
			continue
		}
		if key == cycleKey {
			return id, true, nil
		}
	}
	return "", false, nil
}

// RespawnLeaf quiesces any in-pane viewer for paneID, then stamps the leaf's
// title/options and respawns the pane with the leaf's command. It is the
// targeted single-pane counterpart to ApplyLayout — cycle-scale calls it to
// advance a visible pane to a new replica without rebuilding the whole window.
//
// The s parameter must be a Session controlling the window that contains
// paneID (so s.cfg.ViewerDetachKeys is available for the quiesce step).
func RespawnLeaf(
	ctx context.Context,
	s *Session,
	paneID string,
	leaf muxctl.Leaf,
) error {
	restore := s.quiesceSinglePane(ctx, paneID)
	defer restore()

	paneSpec := muxctl.PaneSpec{Leaf: leaf}
	return s.stampLeaf(ctx, paneID, paneSpec, true, 0)
}

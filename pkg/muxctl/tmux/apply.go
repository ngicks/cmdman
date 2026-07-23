package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// ApplyLayout resets the controlled window and rebuilds root depth-first.
// A nonnegative marker is an opaque layout index persisted on every realized
// pane and read by [Session.StatWindow]; a negative marker skips recording and
// clears stale values. Panes too small to realize are skipped and logged.
func (s *Session) ApplyLayout(
	ctx context.Context,
	root muxctl.PaneSpec,
	marker int,
) (map[string]muxctl.Pane, error) {
	restore := s.quiesceViewers(ctx)
	defer restore()

	anchorID, err := s.resetWindow(ctx)
	if err != nil {
		return nil, fmt.Errorf("tmux: reset window: %w", err)
	}

	w, h, err := s.paneSize(ctx, anchorID)
	if err != nil {
		return nil, fmt.Errorf("tmux: query anchor size: %w", err)
	}

	st := &applyState{
		s:      s,
		ctx:    ctx,
		marker: marker,
		panes:  make(map[string]muxctl.Pane),
	}
	if err := st.materialize(anchorID, root, w, h); err != nil {
		return nil, err
	}
	if len(st.skipped) > 0 {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
			ctx,
			"muxctl/tmux: window too small to fit layout; skipped panes",
			"panes", strings.Join(st.skipped, ", "),
		)
	}

	// Geometry is now final. Respawn the viewers in a second pass so each reads
	// its settled pane size at startup, eliminating the respawn-before-resize
	// race (see realizeLeafAt).
	for _, rl := range st.leaves {
		if err := s.stampLeaf(ctx, rl.paneID, rl.leaf, false, marker); err != nil {
			return nil, err
		}
	}

	focusName := muxctl.PickFocus(root)
	if focusName != "" {
		if p, ok := st.panes[focusName]; ok {
			if _, err := s.exec.run(ctx, "select-pane", "-t", p.PaneId()); err != nil {
				return nil, fmt.Errorf("tmux: select focus pane %q: %w", focusName, err)
			}
		}
	}

	return st.panes, nil
}

type applyState struct {
	s       *Session
	ctx     context.Context
	marker  int
	panes   map[string]muxctl.Pane
	leaves  []realizedLeaf // leaves to respawn after geometry settles (see realizeLeafAt)
	skipped []string
}

// realizedLeaf is a leaf pane whose geometry is placed but whose viewer respawn
// is deferred until the whole window is built.
type realizedLeaf struct {
	paneID string
	leaf   muxctl.PaneSpec
}

func (st *applyState) materialize(anchorID string, node muxctl.PaneSpec, w, h int) error {
	if node.IsLeaf() {
		return st.realizeLeafAt(anchorID, node)
	}
	cells := muxctl.ComputeChildCells(muxctl.ParentDim(node.Dir, w, h), node.Splits)

	last := len(node.Panes) - 1
	for i, child := range node.Panes {
		childW, childH := muxctl.ChildDims(node.Dir, w, h, cells[i])

		if i == last {
			if err := st.materialize(anchorID, child, childW, childH); err != nil {
				return err
			}
			continue
		}

		if cells[i] < 1 {
			st.recordSkipped(child)
			continue
		}

		newID, err := st.split(anchorID, node.Dir, cells[i])
		if err != nil {
			return err
		}
		if err := st.materialize(newID, child, childW, childH); err != nil {
			return err
		}
	}
	return nil
}

// markerOption records the applied layout index independently of pane titles.
const markerOption = "@cmdman_marker"

// leafOption records the cycle-scale command key.
const leafOption = "@cmdman_leaf"

func (st *applyState) realizeLeafAt(paneID string, leaf muxctl.PaneSpec) error {
	// Starting viewers before later splits can leave them with stale geometry.
	st.leaves = append(st.leaves, realizedLeaf{paneID: paneID, leaf: leaf})
	st.panes[leaf.Name] = &Pane{id: paneID, name: leaf.Name}
	return nil
}

func (st *applyState) split(targetID string, dir muxctl.Direction, cells int) (string, error) {
	flag := "-h"
	if dir == muxctl.DirVertical {
		flag = "-v"
	}
	out, err := st.s.exec.run(
		st.ctx,
		"split-window", flag, "-b", "-d",
		"-l", strconv.Itoa(cells),
		"-t", targetID,
		"-P", "-F", "#{pane_id}",
	)
	if err != nil {
		return "", fmt.Errorf("tmux: split-window %s on %s: %w", flag, targetID, err)
	}
	return strings.TrimSpace(out), nil
}

func (st *applyState) recordSkipped(node muxctl.PaneSpec) {
	st.skipped = muxctl.AppendLeafNames(st.skipped, node)
}

// resetWindow reduces the window to its first pane and returns that anchor.
func (s *Session) resetWindow(ctx context.Context) (string, error) {
	out, err := s.exec.run(
		ctx, "list-panes", "-t", s.windowID, "-F", "#{pane_id}",
	)
	if err != nil {
		return "", err
	}
	ids := strings.Split(out, "\n")
	if len(ids) == 0 || ids[0] == "" {
		return "", fmt.Errorf("tmux: window %s has no panes", s.windowID)
	}
	for _, id := range ids[1:] {
		if _, err := s.exec.run(ctx, "kill-pane", "-t", id); err != nil {
			return "", fmt.Errorf("tmux: kill stale pane %s: %w", id, err)
		}
	}
	return ids[0], nil
}

// respawnPane kills the prior pane process and executes argv directly, without
// shell or quoting interpretation.
func (s *Session) respawnPane(ctx context.Context, paneID string, argv []string) error {
	args := []string{"respawn-pane", "-k", "-t", paneID, "--"}
	args = append(args, argv...)
	_, err := s.exec.run(ctx, args...)
	return err
}

// paneSize returns the width and height (in cells) of paneID.
func (s *Session) paneSize(ctx context.Context, paneID string) (int, int, error) {
	out, err := s.exec.run(
		ctx, "display-message", "-t", paneID, "-p",
		"#{pane_width}\t#{pane_height}",
	)
	if err != nil {
		return 0, 0, err
	}
	parts := strings.SplitN(strings.TrimSpace(out), "\t", 2)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("tmux: bad pane size output %q", out)
	}
	w, errW := strconv.Atoi(parts[0])
	h, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil {
		return 0, 0, fmt.Errorf("tmux: parse pane size %q", out)
	}
	return w, h, nil
}

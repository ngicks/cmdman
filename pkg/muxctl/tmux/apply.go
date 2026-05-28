package tmux

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// ApplyLayout resets the cmdman-owned window and rebuilds it from root.
//
// Algorithm:
//
//  1. Reset the window to a single anchor pane (kill all others).
//
//  2. Query the anchor pane's width/height for cell-budget computation.
//
//  3. Walk root depth-first:
//
//     - At a leaf, respawn the anchor pane with the leaf's argv and set
//     its pane-border title.
//     - At a container, peel each non-last child off the anchor with
//     split-window (in node.Dir, taking cells[i] of the anchor's split
//     dim); the last child takes over the anchor.
//
//  4. Select the focused pane (first leaf with Focus=true; otherwise the
//     first leaf in document order).
//
// If a child's computed cell budget is < 1, the child is skipped and the
// dropped pane names are emitted via the context-scoped logger
// (contextkey.ValueSlogLoggerDefault). This implements the plan's
// best-effort behavior for under-sized terminals.
func (s *Session) ApplyLayout(
	ctx context.Context,
	root muxctl.PaneSpec,
) (map[string]muxctl.Pane, error) {
	anchorID, err := s.resetWindow(ctx)
	if err != nil {
		return nil, fmt.Errorf("tmux: reset window: %w", err)
	}

	w, h, err := s.paneSize(ctx, anchorID)
	if err != nil {
		return nil, fmt.Errorf("tmux: query anchor size: %w", err)
	}

	st := &applyState{
		s:     s,
		ctx:   ctx,
		panes: make(map[string]muxctl.Pane),
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

	focusName := pickFocus(root)
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
	panes   map[string]muxctl.Pane
	skipped []string
}

func (st *applyState) materialize(anchorID string, node muxctl.PaneSpec, w, h int) error {
	if node.IsLeaf() {
		return st.realizeLeafAt(anchorID, node)
	}
	cells := computeChildCells(parentDim(node.Dir, w, h), node.Splits)

	last := len(node.Panes) - 1
	for i, child := range node.Panes {
		childW, childH := childDims(node.Dir, w, h, cells[i])

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

func (st *applyState) realizeLeafAt(paneID string, leaf muxctl.PaneSpec) error {
	if err := st.s.respawnPane(st.ctx, paneID, leaf.Cmd); err != nil {
		return fmt.Errorf("tmux: respawn pane %s with %v: %w", paneID, leaf.Cmd, err)
	}
	title := cmp.Or(leaf.CmdOpt["title"], leaf.Name)
	if _, err := st.s.exec.run(
		st.ctx, "select-pane", "-t", paneID, "-T", title,
	); err != nil {
		return fmt.Errorf("tmux: set pane title for %s: %w", paneID, err)
	}
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

// recordSkipped records every leaf name under skipped (for the warn line).
func (st *applyState) recordSkipped(node muxctl.PaneSpec) {
	if node.IsLeaf() {
		st.skipped = append(st.skipped, node.Name)
		return
	}
	for _, c := range node.Panes {
		st.recordSkipped(c)
	}
}

// resetWindow kills every pane in the cmdman-owned window except the
// first one (in tmux's list order) and returns the surviving pane's id.
// The survivor is then used as the apply anchor.
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

// respawnPane replaces the in-pane process with argv (killing the
// previous process via -k). respawn-pane spawns argv directly, not
// through a shell, so quoting concerns do not apply to argv elements.
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

// parentDim returns the parent-pane dimension along the container's split
// direction: width for horizontal, height for vertical.
func parentDim(dir muxctl.Direction, w, h int) int {
	if dir == muxctl.DirVertical {
		return h
	}
	return w
}

// childDims returns the (width, height) a child gets given the container's
// direction, the parent's dims, and the child's allocated cells.
func childDims(dir muxctl.Direction, parentW, parentH, childCells int) (int, int) {
	if dir == muxctl.DirVertical {
		return parentW, childCells
	}
	return childCells, parentH
}

// pickFocus returns the name of the leaf to focus: the first leaf with
// Focus=true, or the first leaf in document order. Returns "" if the tree
// contains no leaves (impossible after Validate, but safe).
func pickFocus(root muxctl.PaneSpec) string {
	var first string
	var focused string
	var walk func(p muxctl.PaneSpec)
	walk = func(p muxctl.PaneSpec) {
		if focused != "" {
			return
		}
		if p.IsLeaf() {
			if first == "" {
				first = p.Name
			}
			if p.Focus {
				focused = p.Name
			}
			return
		}
		for _, c := range p.Panes {
			walk(c)
			if focused != "" {
				return
			}
		}
	}
	walk(root)
	if focused != "" {
		return focused
	}
	return first
}

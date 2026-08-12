package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// ApplyLayout resets the controlled window's project region and rebuilds root
// depth-first inside it.
// A nonnegative marker is an opaque layout index persisted on every realized
// pane and read by [Session.StatWindow]; a negative marker skips recording and
// clears stale values. Panes too small to realize are skipped and logged.
//
// Frame-stamped panes (frameOption) are not part of the project layout: they
// survive the reset, the rebuild is anchored on the project region they leave
// over, and they are never the pane left focused — [muxctl.PickFocus] chooses
// among the layout's own leaves, and whatever the reset did to the active pane
// is settled afterwards. A window carrying no frame panes is reset whole,
// exactly as before.
func (s *Session) ApplyLayout(
	ctx context.Context,
	root muxctl.PaneSpec,
	marker int,
) (map[string]muxctl.Pane, error) {
	restore := s.quiesceViewers(ctx)
	defer restore()

	anchorID, framed, err := s.resetWindow(ctx)
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
		if err := s.stampLeaf(ctx, rl.paneID, rl.leaf, false, marker, false); err != nil {
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
	// The focus the layout asks for is only as good as the panes it names: a
	// focus leaf skipped for lack of room, or a layout naming none, selects
	// nothing — and the reset above can have left the active pane on a frame
	// pane (measured: tmux falls back to the last-active pane, frame or not,
	// when the active one is killed). Settling it here is what makes "focus
	// lands in the project region" hold for every layout, not just the ones
	// that name a leaf. An unframed window has no such pane to land on and is
	// left alone, so nothing changes for a caller without a frame.
	if framed {
		if err := s.focusMainRegion(ctx, anchorID); err != nil {
			return nil, err
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
	// frame builds a frame subtree rather than the project layout, so every
	// pane split off here is stamped as a frame pane the moment it exists.
	frame bool
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
	id := strings.TrimSpace(out)
	if st.frame {
		// A bar holding a subtree is subdivided here, and its children are frame
		// panes too — stamped on creation for the same reason splitFullWindow
		// stamps the bars themselves.
		if err := st.s.stampFramePane(st.ctx, id); err != nil {
			return "", err
		}
	}
	return id, nil
}

func (st *applyState) recordSkipped(node muxctl.PaneSpec) {
	st.skipped = muxctl.AppendLeafNames(st.skipped, node)
}

// resetWindow reduces the window to its first project pane and returns that
// anchor, plus whether the window carries a frame. Frame panes are spared, so
// the anchor left over is the project region and the rebuild stays inside it;
// on an unframed window every pane is a project pane and this is the
// whole-window reset it has always been.
//
// A framed window with no project pane left — its viewers exited under a frame
// that kept the window open — is not a dead end: the main region is spawned
// back (see spawnMainRegion) and the rebuild proceeds inside it. That is the
// same seam a frame shown before anything was launched sits on, and the reason
// an apply can follow a show in either order.
//
// framed is what tells a caller whether it must settle the focus afterwards:
// killing the active pane makes tmux fall back to the last-active one, which on
// a framed window can be a frame pane. It is meaningful only alongside a nil
// error; a failed reset reports false and leaves the window as it found it.
func (s *Session) resetWindow(ctx context.Context) (anchorID string, framed bool, err error) {
	panes, err := listPanesByRole(ctx, s.exec, s.windowID)
	if err != nil {
		return "", false, err
	}
	if len(panes.project) == 0 {
		if len(panes.frame) == 0 {
			return "", false, fmt.Errorf("tmux: window %s has no panes", s.windowID)
		}
		anchorID, err := s.spawnMainRegion(ctx)
		if err != nil {
			return "", false, err
		}
		return anchorID, true, nil
	}
	for _, id := range panes.project[1:] {
		if _, err := s.exec.run(ctx, "kill-pane", "-t", id); err != nil {
			return "", false, fmt.Errorf("tmux: kill stale pane %s: %w", id, err)
		}
	}
	return panes.project[0], len(panes.frame) > 0, nil
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
	return parseCellPair(out)
}

// windowSize returns the width and height (in cells) of the controlled window.
// A frame divides the window rather than the pane it is anchored on, so its
// arithmetic starts here instead of at paneSize.
func (s *Session) windowSize(ctx context.Context) (int, int, error) {
	out, err := s.exec.run(
		ctx, "display-message", "-t", s.windowID, "-p",
		"#{window_width}\t#{window_height}",
	)
	if err != nil {
		return 0, 0, err
	}
	return parseCellPair(out)
}

// parseCellPair reads a tab-separated width/height pair as emitted by the
// display-message formats above.
func parseCellPair(out string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(out), "\t", 2)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("tmux: bad size output %q", out)
	}
	w, errW := strconv.Atoi(parts[0])
	h, errH := strconv.Atoi(parts[1])
	if errW != nil || errH != nil {
		return 0, 0, fmt.Errorf("tmux: parse size %q", out)
	}
	return w, h, nil
}

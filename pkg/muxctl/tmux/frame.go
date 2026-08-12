package tmux

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/go-common/contextkey"
)

// frameOption is the per-pane tmux user option that marks a pane as part of a
// shown frame rather than of the project layout. Readers treat any non-empty
// value as "frame pane", so the value stays free for a later frame revision to
// carry more than membership. Frame panes never carry markerOption or
// leafOption: both are project-layout vocabulary, and the scans that read them
// cover every pane in the window.
//
// Like markerOption and leafOption, this is a tmux user option and has no
// equivalent in zellij or wezterm — same portability caveat as scale_state.go
// records for the per-window state.
const frameOption = "@cmdman_frame"

// frameStamp is the value written into frameOption; only its non-emptiness is
// meaningful to readers.
const frameStamp = "1"

// frameDefOption is the window-level tmux user option holding the name of the
// frame def currently shown around the window (state key
// [muxctl.StateKeyFrameDef]). It equals stateOption(muxctl.StateKeyFrameDef);
// it is named here so [Session.ShowFrame] and [Session.HideFrame] can address
// it directly, as scaleOption is for the scale state.
//
// Window-level: a frame's record survives pane churn and is readable on a
// window whose panes answer nothing — the pane stamps say which panes are the
// frame, this says which frame they are.
const frameDefOption = userOptionPrefix + "frame_def"

// windowPanes holds one window's panes in tmux list order, split by the role
// their stamps give them. Dead panes are included: a window reset must kill a
// stale project pane whether or not its viewer already exited.
type windowPanes struct {
	project []string
	frame   []string
}

// listPanesByRole reads windowID's panes and splits them by frameOption. A pane
// with no stamp is a project pane, so a window that was never framed yields
// every pane in project and nothing in frame.
func listPanesByRole(ctx context.Context, e *executor, windowID string) (windowPanes, error) {
	out, err := e.run(
		ctx, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{"+frameOption+"}",
	)
	if err != nil {
		return windowPanes{}, fmt.Errorf("tmux: list panes for %s: %w", windowID, err)
	}
	if out == "" {
		return windowPanes{}, nil
	}
	var panes windowPanes
	for line := range strings.SplitSeq(out, "\n") {
		// A line without the trailing field is an unstamped pane: tmux versions
		// disagree on whether a trailing empty field is emitted or dropped.
		id, stamp, _ := strings.Cut(line, "\t")
		switch {
		case id == "":
			continue
		case stamp != "":
			panes.frame = append(panes.frame, id)
		default:
			panes.project = append(panes.project, id)
		}
	}
	return panes, nil
}

// mainRegionShare is how much of the frame pane a spawned main region takes
// back — the bulk, not half. The pane being split is the one tmux handed the
// main region's space to when it died, so the bulk is what is being returned;
// and an even split of a bar that never grew leaves a one-row main region, or
// is refused outright (measured on tmux 3.7b: "no space for a new pane").
const mainRegionShare = "80%"

// spawnMainRegion carves a default pane out of the frame for a window that
// holds nothing else: the project's viewers exited under a frame that kept the
// window open, or a frame was shown before anything was launched and its main
// region has since gone. Both [Session.ApplyLayout] and [Session.ShowFrame]
// need a project pane to anchor on, and "an empty main region is the driver's
// default pane" is what says where it comes from — the driver spawns one rather
// than refusing a window it can still serve.
//
// The largest frame pane is the one split: with the main region gone tmux has
// already parcelled its space out to the survivors, so the largest is where
// most of it went. The split runs across that pane's shorter side, which leaves
// a bar shaped like a bar rather than a strip. The new pane carries no stamp —
// tmux does not inherit pane options across a split — so it is a project pane
// by the same rule every other unstamped pane is.
func (s *Session) spawnMainRegion(ctx context.Context) (string, error) {
	out, err := s.exec.run(
		ctx, "list-panes", "-t", s.windowID,
		"-F", "#{pane_id}\t#{"+frameOption+"}\t#{pane_width}\t#{pane_height}",
	)
	if err != nil {
		return "", fmt.Errorf("tmux: list panes for %s: %w", s.windowID, err)
	}
	var target string
	var targetW, targetH, largest int
	for line := range strings.SplitSeq(out, "\n") {
		id, rest, _ := strings.Cut(line, "\t")
		stamp, size, _ := strings.Cut(rest, "\t")
		if id == "" || stamp == "" {
			continue
		}
		w, h, err := parseCellPair(size)
		if err != nil {
			continue
		}
		if w*h > largest {
			target, targetW, targetH, largest = id, w, h, w*h
		}
	}
	if target == "" {
		// Reachable only if the frame panes the caller just saw are gone: there
		// is nothing left to carve a region out of, and nothing to report but
		// the window itself.
		return "", fmt.Errorf(
			"tmux: window %s has no frame pane to spawn a main region from",
			s.windowID,
		)
	}
	flag := "-v"
	if targetW < targetH {
		flag = "-h"
	}
	id, err := s.exec.run(
		ctx, "split-window", flag, "-d", "-l", mainRegionShare,
		"-t", target, "-P", "-F", "#{pane_id}",
	)
	if err != nil {
		return "", fmt.Errorf("tmux: spawn main region beside %s: %w", target, err)
	}
	return strings.TrimSpace(id), nil
}

// ShowFrame realizes the frame tree root around what the window already holds.
// The pane named mainName stands for that content: it is the region every other
// subtree is carved out of, and it is only resized — never killed, rebuilt, or
// respawned. defName is recorded in frameDefOption.
//
// The window's unstamped panes are the main region, so a window that never had
// a layout applied frames the pane it was created with; a window left holding
// nothing but frame panes gets a default one (see spawnMainRegion).
func (s *Session) ShowFrame(
	ctx context.Context,
	root muxctl.PaneSpec,
	mainName, defName string,
) error {
	if mainName == "" {
		return errors.New("tmux: ShowFrame: mainName is required")
	}
	if defName == "" {
		return errors.New("tmux: ShowFrame: defName is required")
	}

	panes, err := listPanesByRole(ctx, s.exec, s.windowID)
	if err != nil {
		return err
	}
	anchorID := ""
	if len(panes.project) > 0 {
		anchorID = panes.project[0]
	} else {
		anchorID, err = s.spawnMainRegion(ctx)
		if err != nil {
			return err
		}
	}

	w, h, err := s.windowSize(ctx)
	if err != nil {
		return fmt.Errorf("tmux: query window size: %w", err)
	}
	plan, ok := planFrame(root, mainName, w, h)
	if !ok {
		return fmt.Errorf("tmux: ShowFrame: no pane named %q in the frame tree", mainName)
	}

	// Stamped before the panes exist: a show that fails halfway leaves frame
	// panes behind, and HideFrame is what removes them — a window that says it
	// holds no frame would strand them. Each pane is likewise stamped as it is
	// created (see splitFullWindow and [applyState.split]), so every state a
	// half-finished show can leave is one HideFrame recognizes in full.
	if _, err := s.exec.run(
		ctx, "set-option", "-w", "-t", s.windowID, frameDefOption, defName,
	); err != nil {
		return fmt.Errorf("tmux: stamp %s: %w", frameDefOption, err)
	}

	st := &applyState{
		s:      s,
		ctx:    ctx,
		marker: -1,
		panes:  make(map[string]muxctl.Pane),
		frame:  true,
	}
	// Outermost first in plan order, so created last: a full split attaches at
	// the window's root, so the newest one encloses every earlier one. That is
	// the nesting the carve describes — each entry divides what its predecessors
	// left over.
	for i, p := range slices.Backward(plan) {
		if p.cells < 1 {
			st.recordSkipped(p.node)
			continue
		}
		id, err := s.splitFullWindow(ctx, anchorID, p.dir, p.before, p.cells)
		if err != nil {
			return err
		}
		plan[i].paneID = id
	}

	// Geometry of the bars themselves is settled; only now subdivide the ones
	// holding a subtree, and only then respawn (see [applyState.realizeLeafAt]).
	for _, p := range plan {
		if p.paneID == "" {
			continue
		}
		if err := st.materialize(p.paneID, p.node, p.w, p.h); err != nil {
			return err
		}
	}
	if len(st.skipped) > 0 {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
			ctx,
			"muxctl/tmux: window too small to fit frame; skipped panes",
			"panes", strings.Join(st.skipped, ", "),
		)
	}
	for _, rl := range st.leaves {
		if err := s.stampLeaf(ctx, rl.paneID, rl.leaf, false, -1, true); err != nil {
			return err
		}
	}

	return s.focusMainRegion(ctx, anchorID)
}

// HideFrame is the frame side's teardown: it kills every frame-stamped pane and
// clears frameDefOption, leaving the main region to expand into the window the
// frame gave back. Frame panes are killed alike: what a frame pane was running
// is the consumer's business, and the viewer quiesce is deliberately not run —
// its detach keys address the project's viewers, which this operation does not
// touch. What the main region runs is never changed: hiding a frame is not a
// project teardown, and [Session.Detach] is the call that is.
//
// When the frame was the last cmdman state on the window, the window itself is
// given back too (see releaseWindowIfLast). A window holding nothing but the
// frame — its main pane exited, or a project teardown left it empty — would
// close outright if every pane were killed, so the last frame pane becomes the
// default pane instead: the same clean window the restore ends at either way.
func (s *Session) HideFrame(ctx context.Context) error {
	// A window carrying nothing of cmdman's is nobody's to give back: falling
	// through to releaseWindowIfLast would unset a pane-border-status the user
	// set for themselves. Hiding a frame that was never shown changes nothing.
	state, err := s.hasCmdmanState(ctx)
	if err != nil {
		return err
	}
	if !state {
		return nil
	}

	panes, err := listPanesByRole(ctx, s.exec, s.windowID)
	if err != nil {
		return err
	}
	kill := panes.frame
	if len(panes.frame) > 0 && len(panes.project) == 0 {
		survivor := panes.frame[0]
		kill = panes.frame[1:]
		if err := s.respawnPane(ctx, survivor, []string{s.defaultShell(ctx)}); err != nil {
			return fmt.Errorf("tmux: respawn shell in frame pane %s: %w", survivor, err)
		}
		_, _ = s.exec.run(ctx, "select-pane", "-t", survivor, "-T", "")
		// Checked, unlike the title above: a survivor that keeps its stamp is a
		// window whose last pane answers to the frame while the frame's own state
		// is gone — the half-stamped state per-side teardown exists to rule out.
		if _, err := s.exec.run(
			ctx, "set-option", "-p", "-u", "-t", survivor, frameOption,
		); err != nil {
			return fmt.Errorf("tmux: clear %s on %s: %w", frameOption, survivor, err)
		}
	}
	for _, id := range kill {
		if _, err := s.exec.run(ctx, "kill-pane", "-t", id); err != nil {
			return fmt.Errorf("tmux: kill frame pane %s: %w", id, err)
		}
	}
	if _, err := s.exec.run(
		ctx, "set-option", "-w", "-u", "-t", s.windowID, frameDefOption,
	); err != nil {
		return fmt.Errorf("tmux: clear %s: %w", frameDefOption, err)
	}
	return s.releaseWindowIfLast(ctx)
}

// framePlacement is one frame subtree of a frame tree together with the split
// that carves it out of the rectangle its predecessors left over, and the size
// that rectangle affords it.
type framePlacement struct {
	node   muxctl.PaneSpec
	dir    muxctl.Direction
	before bool // the subtree leads the main region rather than trailing it
	cells  int
	w, h   int
	paneID string
}

// planFrame walks root outside-in and returns one placement per subtree that is
// not the main region, in that order. ok reports whether mainName was found: a
// tree without it describes no main region, so there is nothing to frame.
//
// Sizes come out of the same arithmetic [applyState.materialize] uses, against
// the rectangle remaining at each level — which is what makes a percentage
// resolve against the remainder rather than the whole window.
func planFrame(root muxctl.PaneSpec, mainName string, w, h int) (plan []framePlacement, ok bool) {
	// Matched by name before shape: the main leaf is a placeholder standing for
	// panes that already exist, so a caller need not give it an argv it will
	// never run.
	if root.Name == mainName {
		return nil, true
	}
	if root.IsLeaf() {
		return nil, false
	}
	mainIdx := -1
	for i, child := range root.Panes {
		if containsPaneNamed(child, mainName) {
			mainIdx = i
			break
		}
	}
	if mainIdx < 0 {
		return nil, false
	}

	cells := muxctl.ComputeChildCells(muxctl.ParentDim(root.Dir, w, h), root.Splits)
	// A tree holding the main placeholder cannot pass [muxctl.MuxSpec.Validate] —
	// the placeholder is neither leaf nor container — so the splits/panes mismatch
	// Validate rejects arrives here instead. Read the uncovered children as too
	// small to realize rather than indexing past the computed cells.
	if len(cells) < len(root.Panes) {
		cells = append(cells, make([]int, len(root.Panes)-len(cells))...)
	}
	for i, child := range root.Panes {
		if i == mainIdx {
			continue
		}
		childW, childH := muxctl.ChildDims(root.Dir, w, h, cells[i])
		plan = append(plan, framePlacement{
			node:   child,
			dir:    root.Dir,
			before: i < mainIdx,
			cells:  cells[i],
			w:      childW,
			h:      childH,
		})
	}
	mainW, mainH := muxctl.ChildDims(root.Dir, w, h, cells[mainIdx])
	deeper, _ := planFrame(root.Panes[mainIdx], mainName, mainW, mainH)
	return append(plan, deeper...), true
}

// containsPaneNamed reports whether name occurs anywhere under node.
func containsPaneNamed(node muxctl.PaneSpec, name string) bool {
	if node.Name == name {
		return true
	}
	for _, child := range node.Panes {
		if containsPaneNamed(child, name) {
			return true
		}
	}
	return false
}

// splitFullWindow docks a pane of cells cells against an edge of the WINDOW
// (split-window -f), not of targetID: the main region may already be several
// panes, and a frame bar spans all of them. before puts the new pane ahead of
// the existing content — the top or left edge — instead of after it.
//
// The pane is stamped as a frame pane before the id is handed back, so it is
// never briefly indistinguishable from a project pane: a show that dies between
// two docks leaves panes [Session.HideFrame] removes, and a leading bar that
// nothing recognized would otherwise be adopted as the anchor by the next
// resetWindow — which would kill the project panes it sorts ahead of.
func (s *Session) splitFullWindow(
	ctx context.Context,
	targetID string,
	dir muxctl.Direction,
	before bool,
	cells int,
) (string, error) {
	flag := "-h"
	if dir == muxctl.DirVertical {
		flag = "-v"
	}
	args := []string{"split-window", flag, "-f", "-d"}
	if before {
		args = append(args, "-b")
	}
	args = append(
		args,
		"-l", strconv.Itoa(cells),
		"-t", targetID,
		"-P", "-F", "#{pane_id}",
	)
	out, err := s.exec.run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("tmux: split-window %s -f on %s: %w", flag, targetID, err)
	}
	id := strings.TrimSpace(out)
	if err := s.stampFramePane(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

// stampFramePane marks paneID as belonging to the shown frame. It writes the
// membership stamp alone: the title, the respawn and the marker/leaf clearing a
// realized frame leaf also wants are [Session.stampLeaf]'s, and they wait until
// the geometry is settled.
func (s *Session) stampFramePane(ctx context.Context, paneID string) error {
	if _, err := s.exec.run(
		ctx, "set-option", "-p", "-t", paneID, frameOption, frameStamp,
	); err != nil {
		return fmt.Errorf("tmux: stamp frame pane %s: %w", paneID, err)
	}
	return nil
}

// focusMainRegion selects anchorID unless the window's active pane is already a
// project pane. It is the one place the driver enforces "a frame pane is never
// a focus candidate": every operation that can leave the active pane on a frame
// pane ends here, and an operation that already left it on a project pane pays
// only the two queries and moves nothing.
//
// The ways focus reaches a frame pane are not the same in each caller —
// [Session.ShowFrame] can dock around a window whose active pane the user had
// already put on an earlier frame's pane, while an apply or a collapse kills
// the active project pane and tmux falls back to the last-active one — so the
// check is on where the focus IS, not on how it got there.
func (s *Session) focusMainRegion(ctx context.Context, anchorID string) error {
	active, err := s.exec.run(
		ctx, "display-message", "-t", s.windowID, "-p", "#{pane_id}",
	)
	if err == nil {
		panes, listErr := listPanesByRole(ctx, s.exec, s.windowID)
		if listErr == nil && slices.Contains(panes.project, strings.TrimSpace(active)) {
			return nil
		}
	}
	if _, err := s.exec.run(ctx, "select-pane", "-t", anchorID); err != nil {
		return fmt.Errorf("tmux: select main region pane %s: %w", anchorID, err)
	}
	return nil
}

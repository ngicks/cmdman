package mux

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// CycleScaleOptions configures [CycleScale].
type CycleScaleOptions struct {
	// Spec is the cmdman-layer layout spec.
	Spec Spec
	// Resolver maps (command, scaleIndex) → cmdman command id.
	Resolver Resolver
	// Replicas reports how many replicas a command has.
	Replicas ReplicaCounter
	// Opts carries the per-pane argv parameters.
	Opts PaneArgvOpts
	// Identity is the ownership identity string to find windows.
	Identity string
	// SessionName, when non-empty, narrows discovery to that session.
	SessionName string
	// Command is the compose service name to advance.
	Command string
	// Position is the target replica (1-based). 0 means "advance by one".
	Position int
}

// CycleScaleWindowResult is the per-window result from [CycleScale].
type CycleScaleWindowResult struct {
	// SessionName is the tmux session the window belongs to.
	SessionName string
	// WindowName is the human-visible window name.
	WindowName string
	// WindowID is the tmux window id.
	WindowID string
	// Command is the command that was cycled.
	Command string
	// OldPosition is the position before cycling.
	OldPosition int
	// NewPosition is the position after cycling.
	NewPosition int
	// ResolvedName is the replica name (e.g. "web-2").
	ResolvedName string
	// Visible reports whether the pane was found and respawned.
	Visible bool
	// LayoutName is the current layout name (from the marker).
	LayoutName string
}

// CycleScaleResult is the aggregate result from [CycleScale].
type CycleScaleResult struct {
	Results []CycleScaleWindowResult
}

// ScaleStateOptions configures [ReadScaleState].
type ScaleStateOptions struct {
	Driver    string
	DriverOpt map[string]string
	// SessionName narrows discovery.
	SessionName string
	// Identity filters windows by ownership stamp.
	Identity string
	Env      []string
}

// CycleScale advances the replica position for opts.Command across all matching
// cmdman-owned windows. It finds each window by identity, computes the next
// (or explicit) replica position skipping positions pinned by other leaves in
// the current layout, respawns the visible pane, and persists the new position.
//
// A partial result plus a non-nil error is returned when some windows succeed
// and others fail — the caller can inspect CycleScaleResult.Results for
// successful windows and the returned error for all collected failures.
func CycleScale(ctx context.Context, opts CycleScaleOptions) (CycleScaleResult, error) {
	// Step 1: static target check — opts.Command must appear as an unpinned
	// leaf (Scale == 0) with a non-nil ReplicaCounter in at least one layout.
	if !isCycleScaleTarget(opts.Spec, opts.Command, opts.Replicas) {
		return CycleScaleResult{}, fmt.Errorf(
			"mux: %q is not a cycle-scale target: not an unpinned leaf in any layout",
			opts.Command,
		)
	}

	// Step 2: resolve driver and find windows.
	driver := resolveDriver(opts.Spec.Driver, os.Environ())
	if driver != "tmux" {
		return CycleScaleResult{}, fmt.Errorf(
			"mux: driver %q is not implemented yet (v1 ships tmux only)", driver,
		)
	}

	rows, err := tmux.ListOwnedWindows(ctx, tmux.ListOwnedWindowsOptions{
		Path:     opts.Spec.DriverOpt["path"],
		Socket:   opts.Spec.DriverOpt["socket"],
		Session:  opts.SessionName,
		Identity: opts.Identity,
	})
	if err != nil {
		return CycleScaleResult{}, fmt.Errorf("mux: enumerate owned windows: %w", err)
	}
	if len(rows) == 0 {
		return CycleScaleResult{}, fmt.Errorf(
			"mux: no dashboard window found; run \"cmdman compose mux up\" first",
		)
	}

	// Step 3: per-window loop.
	var (
		results []CycleScaleWindowResult
		errs    []error
	)

	listOpts := tmux.ListOwnedWindowsOptions{
		Path:   opts.Spec.DriverOpt["path"],
		Socket: opts.Spec.DriverOpt["socket"],
	}

	for _, window := range rows {
		res, cycleErr := cycleScaleWindow(ctx, opts, window, listOpts)
		if cycleErr != nil {
			errs = append(errs, cycleErr)
			// Still append a partial result when available.
			if res.WindowID != "" {
				results = append(results, res)
			}
			continue
		}
		results = append(results, res)
	}

	return CycleScaleResult{Results: results}, errors.Join(errs...)
}

// cycleScaleWindow processes a single owned window: validates the marker, looks
// up replica count, computes the new position, opens the session, respawns the
// pane (when visible), and persists the position.
func cycleScaleWindow(
	ctx context.Context,
	opts CycleScaleOptions,
	window tmux.OwnedWindow,
	listOpts tmux.ListOwnedWindowsOptions,
) (CycleScaleWindowResult, error) {
	base := CycleScaleWindowResult{
		SessionName: window.SessionName,
		WindowName:  window.WindowName,
		WindowID:    window.WindowID,
		Command:     opts.Command,
	}

	// 3a: validate marker index.
	if window.Marker < 0 || window.Marker >= len(opts.Spec.Layouts) {
		return base, fmt.Errorf(
			"mux: window %s (%s in session %s): marker %d out of range [0,%d)",
			window.WindowName, window.WindowID, window.SessionName,
			window.Marker, len(opts.Spec.Layouts),
		)
	}

	// 3b: current layout.
	currentLayout := opts.Spec.Layouts[window.Marker]
	base.LayoutName = currentLayout.Name

	// 3d: live replica count.
	n, err := opts.Replicas(ctx, opts.Command)
	if err != nil {
		return base, fmt.Errorf(
			"mux: window %s: count replicas of %q: %w",
			window.WindowID, opts.Command, err,
		)
	}
	n = max(n, 1)

	// 3e: current stored position (default 1), wrapped into [1,n].
	storedPos := 1
	if window.ScalePositions != nil {
		if sp, ok := window.ScalePositions[opts.Command]; ok {
			storedPos = sp
		}
	}
	curPos := ((storedPos - 1) % n) + 1
	base.OldPosition = curPos

	// 3f: build pinnedIndices for this layout + command.
	pinnedIndices := pinnedScaleIndices(currentLayout, opts.Command)

	// 3f: compute target position.
	targetPos, err := computeTargetPosition(curPos, opts.Position, n, pinnedIndices)
	if err != nil {
		return base, fmt.Errorf(
			"mux: window %s, command %q: %w", window.WindowID, opts.Command, err,
		)
	}
	base.NewPosition = targetPos

	// 3g: resolve replica id.
	id, err := opts.Resolver(ctx, opts.Command, targetPos)
	if err != nil {
		return base, fmt.Errorf(
			"mux: window %s: resolve %q replica %d: %w",
			window.WindowID, opts.Command, targetPos, err,
		)
	}

	// 3h: human-readable replica name.
	resolvedName := fmt.Sprintf("%s-%d", opts.Command, targetPos)
	base.ResolvedName = resolvedName

	// 3i: open existing session.
	sess, ok, openErr := tmux.OpenExisting(ctx, tmux.Config{
		Path:             opts.Spec.DriverOpt["path"],
		Socket:           opts.Spec.DriverOpt["socket"],
		WindowID:         window.WindowID,
		ViewerDetachKeys: viewerDetachKeys,
	})
	if openErr != nil {
		return base, fmt.Errorf(
			"mux: open window %s (%s in session %s): %w",
			window.WindowName, window.WindowID, window.SessionName, openErr,
		)
	}
	if !ok {
		// Window disappeared between ListOwnedWindows and OpenExisting.
		return base, nil
	}

	// 3j: find pane by cycle key.
	paneID, visible, findErr := tmux.FindLeafPane(ctx, listOpts, window.WindowID, opts.Command)
	if findErr != nil {
		return base, fmt.Errorf(
			"mux: window %s: find leaf pane for %q: %w",
			window.WindowID, opts.Command, findErr,
		)
	}
	base.Visible = visible

	// 3k: respawn if visible.
	if visible {
		// Locate the unpinned leaf for this command to get its Mode and CmdOpt.
		leafSpec, found := findUnpinnedLeaf(currentLayout, opts.Command)
		if !found {
			// Should not happen (we verified it's a target), but be safe.
			return base, fmt.Errorf(
				"mux: window %s: unpinned leaf for %q disappeared from layout %q",
				window.WindowID, opts.Command, currentLayout.Name,
			)
		}
		leaf := muxctl.Leaf{
			Name:     resolvedName,
			Cmd:      paneArgv(opts.Opts, leafSpec.Mode, id),
			CmdOpt:   leafSpec.CmdOpt,
			CycleKey: opts.Command,
		}
		if respawnErr := tmux.RespawnLeaf(ctx, sess, paneID, leaf); respawnErr != nil {
			return base, fmt.Errorf(
				"mux: window %s: respawn leaf pane %s: %w",
				window.WindowID, paneID, respawnErr,
			)
		}
	}

	// 3l: persist the new position.
	if writeErr := tmux.WriteScalePosition(
		ctx, listOpts, window.WindowID, opts.Command, targetPos,
	); writeErr != nil {
		return base, fmt.Errorf(
			"mux: window %s: write scale position for %q: %w",
			window.WindowID, opts.Command, writeErr,
		)
	}

	return base, nil
}

// ReadScaleState discovers windows by identity (and optional session narrowing),
// merges their ScalePositions maps (last window wins per command key), and
// returns the merged map. Returns nil, nil when no windows are found.
func ReadScaleState(ctx context.Context, opts ScaleStateOptions) (map[string]int, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}

	driver := resolveDriver(opts.Driver, env)
	if driver != "tmux" {
		return nil, fmt.Errorf(
			"mux: driver %q is not implemented yet (v1 ships tmux only)", driver,
		)
	}

	rows, err := tmux.ListOwnedWindows(ctx, tmux.ListOwnedWindowsOptions{
		Path:     opts.DriverOpt["path"],
		Socket:   opts.DriverOpt["socket"],
		Session:  opts.SessionName,
		Identity: opts.Identity,
	})
	if err != nil {
		return nil, fmt.Errorf("mux: enumerate owned windows: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	merged := make(map[string]int)
	for _, w := range rows {
		maps.Copy(merged, w.ScalePositions)
	}
	if len(merged) == 0 {
		return nil, nil
	}
	return merged, nil
}

// isCycleScaleTarget reports whether command appears as an unpinned leaf
// (Scale == 0) in at least one layout of spec, and replicas is non-nil.
func isCycleScaleTarget(spec Spec, command string, replicas ReplicaCounter) bool {
	if replicas == nil {
		return false
	}
	for _, layout := range spec.Layouts {
		if _, ok := findUnpinnedLeaf(layout, command); ok {
			return true
		}
	}
	return false
}

// findUnpinnedLeaf walks the pane tree rooted at layout.Root and returns the
// first leaf whose Command matches command and whose Scale is 0 (unpinned).
func findUnpinnedLeaf(layout Layout, command string) (PaneSpec, bool) {
	return findUnpinnedLeafInPane(layout.Root, command)
}

// findUnpinnedLeafInPane recursively searches p for an unpinned leaf matching
// command.
func findUnpinnedLeafInPane(p PaneSpec, command string) (PaneSpec, bool) {
	if p.IsLeaf() {
		if p.Command == command && p.Scale == 0 {
			return p, true
		}
		return PaneSpec{}, false
	}
	for _, child := range p.Panes {
		if found, ok := findUnpinnedLeafInPane(child, command); ok {
			return found, true
		}
	}
	return PaneSpec{}, false
}

// pinnedScaleIndices returns the set of Scale values (1-based) for all leaves
// in layout whose Command matches command and whose Scale > 0.
func pinnedScaleIndices(layout Layout, command string) map[int]struct{} {
	m := make(map[int]struct{})
	collectPinnedIndices(layout.Root, command, m)
	return m
}

// collectPinnedIndices walks p and adds any pinned scale index for command into m.
func collectPinnedIndices(p PaneSpec, command string, m map[int]struct{}) {
	if p.IsLeaf() {
		if p.Command == command && p.Scale > 0 {
			m[p.Scale] = struct{}{}
		}
		return
	}
	for _, child := range p.Panes {
		collectPinnedIndices(child, command, m)
	}
}

// computeTargetPosition computes the next position to advance to.
//
//   - curPos is the current 1-based position (already wrapped into [1,n]).
//   - explicitPos is the requested position (0 = advance by one).
//   - n is the live replica count.
//   - pinnedIndices is the set of scale indices pinned by other leaves of the
//     same command in the current layout (1-based).
//
// Returns the new position, or an error when:
//   - explicitPos is out of range (< 1 or > n)
//   - explicitPos is pinned by another leaf
//   - advancing: all indices are pinned (cannot skip all)
func computeTargetPosition(
	curPos, explicitPos, n int,
	pinnedIndices map[int]struct{},
) (int, error) {
	if explicitPos != 0 {
		// Explicit position path.
		if explicitPos < 1 || explicitPos > n {
			return 0, fmt.Errorf(
				"mux: position %d is out of range [1,%d]", explicitPos, n,
			)
		}
		if _, pinned := pinnedIndices[explicitPos]; pinned {
			return 0, fmt.Errorf(
				"mux: position %d is pinned in current layout", explicitPos,
			)
		}
		return explicitPos, nil
	}

	// Advance-by-one path: start at (curPos % n)+1 and walk forward, skipping
	// pinned positions. Error when all n positions are pinned.
	start := (curPos % n) + 1
	for i := range n {
		candidate := ((start - 1 + i) % n) + 1
		if _, pinned := pinnedIndices[candidate]; !pinned {
			return candidate, nil
		}
	}
	return 0, fmt.Errorf("mux: all scale positions for command are pinned in current layout")
}

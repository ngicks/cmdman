package mux

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/ngicks/cmdman/pkg/muxctl"
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
	if !isCycleScaleTarget(opts.Spec, opts.Command, opts.Replicas) {
		return CycleScaleResult{}, fmt.Errorf(
			"mux: %q is not a cycle-scale target: not an unpinned leaf in any layout",
			opts.Command,
		)
	}

	driver, err := resolveDriver(opts.Spec.Driver, os.Environ())
	if err != nil {
		return CycleScaleResult{}, err
	}

	rows, err := driver.ListWindows(ctx, muxctl.ListOptions{
		DriverOpt: opts.Spec.DriverOpt,
		Session:   opts.SessionName,
		Identity:  opts.Identity,
		StateKeys: []muxctl.StateKey{muxctl.StateKeyScale},
	})
	if err != nil {
		return CycleScaleResult{}, fmt.Errorf("mux: enumerate owned windows: %w", err)
	}
	if len(rows) == 0 {
		return CycleScaleResult{}, fmt.Errorf(
			"mux: no dashboard window found; run \"cmdman compose mux up\" first",
		)
	}

	var (
		results []CycleScaleWindowResult
		errs    []error
	)

	listOpts := muxctl.ListOptions{
		DriverOpt: opts.Spec.DriverOpt,
	}

	for _, window := range rows {
		res, cycleErr := cycleScaleWindow(ctx, driver, opts, window, listOpts)
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
	driver muxctl.Driver,
	opts CycleScaleOptions,
	window muxctl.Window,
	listOpts muxctl.ListOptions,
) (CycleScaleWindowResult, error) {
	base := CycleScaleWindowResult{
		SessionName: window.SessionName,
		WindowName:  window.WindowName,
		WindowID:    window.WindowID,
		Command:     opts.Command,
	}

	if window.Marker < 0 || window.Marker >= len(opts.Spec.Layouts) {
		return base, fmt.Errorf(
			"mux: window %s (%s in session %s): marker %d out of range [0,%d)",
			window.WindowName, window.WindowID, window.SessionName,
			window.Marker, len(opts.Spec.Layouts),
		)
	}

	currentLayout := opts.Spec.Layouts[window.Marker]
	base.LayoutName = currentLayout.Name

	n, err := opts.Replicas(ctx, opts.Command)
	if err != nil {
		return base, fmt.Errorf(
			"mux: window %s: count replicas of %q: %w",
			window.WindowID, opts.Command, err,
		)
	}
	n = max(n, 1)

	// 3e: current stored position (default 1), wrapped into [1,n]. The driver
	// hands back the raw @cmdman_scale string; decoding is a mux-layer concern.
	storedPos := 1
	if scalePositions := decodeScalePositions(
		window.State[muxctl.StateKeyScale],
	); scalePositions != nil {
		if sp, ok := scalePositions[opts.Command]; ok {
			storedPos = sp
		}
	}
	curPos := ((storedPos - 1) % n) + 1
	base.OldPosition = curPos

	pinnedIndices := pinnedScaleIndices(currentLayout, opts.Command)

	targetPos, err := computeTargetPosition(curPos, opts.Position, n, pinnedIndices)
	if err != nil {
		return base, fmt.Errorf(
			"mux: window %s, command %q: %w", window.WindowID, opts.Command, err,
		)
	}
	base.NewPosition = targetPos

	id, err := opts.Resolver(ctx, opts.Command, targetPos)
	if err != nil {
		return base, fmt.Errorf(
			"mux: window %s: resolve %q replica %d: %w",
			window.WindowID, opts.Command, targetPos, err,
		)
	}

	resolvedName := fmt.Sprintf("%s-%d", opts.Command, targetPos)
	base.ResolvedName = resolvedName

	sess, ok, openErr := driver.Open(ctx, muxctl.Config{
		DriverOpt:        opts.Spec.DriverOpt,
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
		// Window disappeared between ListWindows and Open.
		return base, nil
	}

	paneID, visible, findErr := driver.FindPane(ctx, listOpts, window.WindowID, opts.Command)
	if findErr != nil {
		return base, fmt.Errorf(
			"mux: window %s: find leaf pane for %q: %w",
			window.WindowID, opts.Command, findErr,
		)
	}
	base.Visible = visible

	if visible {
		leafSpec, found := findUnpinnedLeaf(currentLayout, opts.Command)
		if !found {
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
		if respawnErr := sess.RespawnLeaf(ctx, paneID, leaf); respawnErr != nil {
			return base, fmt.Errorf(
				"mux: window %s: respawn leaf pane %s: %w",
				window.WindowID, paneID, respawnErr,
			)
		}
	}

	if writeErr := writeScalePosition(
		ctx, driver, listOpts, window.WindowID, opts.Command, targetPos,
	); writeErr != nil {
		return base, fmt.Errorf(
			"mux: window %s: write scale position for %q: %w",
			window.WindowID, opts.Command, writeErr,
		)
	}

	return base, nil
}

// writeScalePosition performs the read-modify-write of the driver's scale
// state (state key muxctl.StateKeyScale) for a single command on windowID: it reads
// the current raw value, decodes it, sets cmd to pos (pos <= 0 removes cmd),
// re-encodes, and writes it back through the driver's window-state KV (which
// unsets the state when the encoding is empty). The scale codec and this RMW
// live here rather than in the driver so "scale" semantics stay out of muxctl.
func writeScalePosition(
	ctx context.Context,
	driver muxctl.Driver,
	opts muxctl.ListOptions,
	windowID, cmd string,
	pos int,
) error {
	raw, err := driver.ReadWindowState(ctx, opts, windowID, muxctl.StateKeyScale)
	if err != nil {
		return fmt.Errorf("mux: read scale positions for window %s: %w", windowID, err)
	}
	m := decodeScalePositions(raw)
	if m == nil {
		m = make(map[string]int)
	}
	if pos <= 0 {
		delete(m, cmd)
	} else {
		m[cmd] = pos
	}
	return driver.WriteWindowState(
		ctx,
		opts,
		windowID,
		muxctl.StateKeyScale,
		encodeScalePositions(m),
	)
}

// ReadScaleState discovers windows by identity (and optional session narrowing),
// decodes each window's raw scale option, and merges the results (last window
// wins per command key), returning the merged map. Returns nil, nil when no
// windows are found.
func ReadScaleState(ctx context.Context, opts ScaleStateOptions) (map[string]int, error) {
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}

	driver, err := resolveDriver(opts.Driver, env)
	if err != nil {
		return nil, err
	}

	rows, err := driver.ListWindows(ctx, muxctl.ListOptions{
		DriverOpt: opts.DriverOpt,
		Session:   opts.SessionName,
		Identity:  opts.Identity,
		StateKeys: []muxctl.StateKey{muxctl.StateKeyScale},
	})
	if err != nil {
		return nil, fmt.Errorf("mux: enumerate owned windows: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	merged := make(map[string]int)
	for _, w := range rows {
		maps.Copy(merged, decodeScalePositions(w.State[muxctl.StateKeyScale]))
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

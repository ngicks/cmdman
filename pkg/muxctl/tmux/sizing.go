package tmux

import "github.com/ngicks/cmdman/pkg/muxctl"

// computeChildCells turns a [muxctl.PaneSpec.Splits] array into concrete
// cell counts given the parent pane's size in the split direction.
//
// Algorithm:
//
//  1. Reserve cells for absolute sizes ("Nc") and percent sizes ("N%",
//     resolved as floor(parent*N/100)).
//
//  2. Reserve one cell per inter-pane separator (len(splits) - 1).
//
//  3. Distribute the remaining cells across weighted sizes by ratio.
//
//  4. Hand any rounding remainder to the last weighted child so the cells
//     sum (plus separators) equals parent.
//
// A child whose computed size is < 1 is returned as 0; the caller may
// treat 0 as "skip, too small" and warn (matches the plan's
// best-effort-on-too-small-terminal behavior).
func computeChildCells(parent int, splits []muxctl.Size) []int {
	n := len(splits)
	cells := make([]int, n)
	if n == 0 {
		return cells
	}

	// Pre-resolve absolute and percent sizes; sum weights.
	reserved := make([]int, n)
	sumReserved := 0
	sumWeight := 0
	for i, s := range splits {
		switch {
		case s.Absolute:
			reserved[i] = s.N
			sumReserved += s.N
		case s.Percent:
			reserved[i] = parent * s.N / 100
			sumReserved += reserved[i]
		default:
			sumWeight += s.N
		}
	}

	separators := n - 1
	leftover := max(parent-sumReserved-separators, 0)

	// First pass: assign reserved sizes and proportional shares (rounded
	// down).
	assignedWeighted := 0
	lastWeightedIdx := -1
	for i, s := range splits {
		if s.Absolute || s.Percent {
			cells[i] = reserved[i]
			continue
		}
		if sumWeight == 0 {
			cells[i] = 0
			continue
		}
		cells[i] = leftover * s.N / sumWeight
		assignedWeighted += cells[i]
		lastWeightedIdx = i
	}

	// Hand the rounding remainder to the last weighted child so the cells
	// (plus separators) fill the parent exactly.
	if lastWeightedIdx >= 0 && assignedWeighted < leftover {
		cells[lastWeightedIdx] += leftover - assignedWeighted
	}

	// Clamp any pane that came out < 1 to 0 (caller treats 0 as skip).
	for i := range cells {
		if cells[i] < 1 {
			cells[i] = 0
		}
	}
	return cells
}

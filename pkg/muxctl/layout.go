package muxctl

// ComputeChildCells turns a [PaneSpec.Splits] array into concrete cell counts
// given the parent pane's size in the split direction.
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
func ComputeChildCells(parent int, splits []Size) []int {
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

// ParentDim returns the parent-pane dimension along the container's split
// direction: width for horizontal, height for vertical.
func ParentDim(dir Direction, w, h int) int {
	if dir == DirVertical {
		return h
	}
	return w
}

// ChildDims returns the (width, height) a child gets given the container's
// direction, the parent's dims, and the child's allocated cells.
func ChildDims(dir Direction, parentW, parentH, childCells int) (int, int) {
	if dir == DirVertical {
		return parentW, childCells
	}
	return childCells, parentH
}

// PickFocus returns the name of the leaf to focus: the first leaf with
// Focus=true, or the first leaf in document order. Returns "" if the tree
// contains no leaves (impossible after Validate, but safe).
func PickFocus(root PaneSpec) string {
	var first string
	var focused string
	var walk func(p PaneSpec)
	walk = func(p PaneSpec) {
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

// AppendLeafNames appends every leaf name under node (depth-first, document
// order) to dst and returns the extended slice. Drivers use it to report the
// panes dropped when a terminal is too small to fit the layout.
func AppendLeafNames(dst []string, node PaneSpec) []string {
	if node.IsLeaf() {
		dst = append(dst, node.Name)
		return dst
	}
	for _, c := range node.Panes {
		dst = AppendLeafNames(dst, c)
	}
	return dst
}

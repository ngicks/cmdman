package muxctl

// ComputeChildCells resolves absolute and percentage sizes, then distributes
// remaining cells by weight. Separators consume one cell each, and rounding
// remainder goes to the last weighted child. parent is the parent pane's cell
// count in the split direction; splits is index-parallel to its children. A zero
// result lets the caller skip a child that is too small to realize.
func ComputeChildCells(parent int, splits []Size) []int {
	n := len(splits)
	cells := make([]int, n)
	if n == 0 {
		return cells
	}

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

	if lastWeightedIdx >= 0 && assignedWeighted < leftover {
		cells[lastWeightedIdx] += leftover - assignedWeighted
	}

	for i := range cells {
		if cells[i] < 1 {
			cells[i] = 0
		}
	}
	return cells
}

// ParentDim returns width for horizontal splits and height for vertical ones.
func ParentDim(dir Direction, w, h int) int {
	if dir == DirVertical {
		return h
	}
	return w
}

// ChildDims returns child width and height for an allocated split size.
func ChildDims(dir Direction, parentW, parentH, childCells int) (int, int) {
	if dir == DirVertical {
		return parentW, childCells
	}
	return childCells, parentH
}

// PickFocus returns the first focused leaf, then the first leaf, or "" if none.
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

// AppendLeafNames appends all leaves under node to dst in document order,
// retaining duplicates, and returns the extended slice.
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

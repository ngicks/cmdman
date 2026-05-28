package muxctl

import "fmt"

// Validate checks structural invariants of the spec. It does not contact the
// driver, verify runnability of Cmd, or measure the terminal; it only checks
// shape:
//
//   - Each Layout has a non-empty Name.
//   - Layout names are unique within MuxSpec.Layouts.
//   - For each PaneSpec: it is either a leaf (Cmd set) XOR a container
//     (Dir+Splits+Panes set).
//   - Container Dir is "h" or "v".
//   - Container has at least one child, and len(Splits) == len(Panes).
//   - Leaf Name is non-empty.
//   - Leaf names are unique within their layout.
//   - At most one PaneSpec.Focus == true per layout.
//
// Validate returns the first error encountered, wrapping the package's
// sentinel errors with positional context.
func (s MuxSpec) Validate() error {
	seenLayout := make(map[string]struct{}, len(s.Layouts))
	for i := range s.Layouts {
		l := s.Layouts[i]
		if l.Name == "" {
			return fmt.Errorf("layouts[%d]: %w", i, ErrLayoutNameRequired)
		}
		if _, dup := seenLayout[l.Name]; dup {
			return fmt.Errorf("layouts[%d]: %w: %q", i, ErrDuplicateLayoutName, l.Name)
		}
		seenLayout[l.Name] = struct{}{}

		if err := l.Root.validateShape(); err != nil {
			return fmt.Errorf("layouts[%d] (%q): %w", i, l.Name, err)
		}

		names := make(map[string]struct{})
		focus := 0
		err := walkLeaves(l.Root, func(p PaneSpec) error {
			if _, dup := names[p.Name]; dup {
				return fmt.Errorf("%w: %q", ErrDuplicatePaneName, p.Name)
			}
			names[p.Name] = struct{}{}
			if p.Focus {
				focus++
				if focus > 1 {
					return ErrMultipleFocus
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("layouts[%d] (%q): %w", i, l.Name, err)
		}
	}
	return nil
}

// validateShape checks the leaf-XOR-container invariant, container fields'
// internal consistency, and leaf Name presence, recursively.
func (p PaneSpec) validateShape() error {
	leaf := p.IsLeaf()
	cont := p.IsContainer()
	if leaf == cont {
		// Both false (empty pane) or both true (impossible by definition;
		// kept as a safety net) — neither well-formed.
		return ErrLeafXorContainer
	}
	if leaf {
		if p.Name == "" {
			return ErrLeafNameRequired
		}
		return nil
	}
	// container
	if p.Dir != DirHorizontal && p.Dir != DirVertical {
		return fmt.Errorf("%w: got %q", ErrInvalidDirection, p.Dir)
	}
	if len(p.Panes) == 0 {
		return ErrEmptyContainer
	}
	if len(p.Splits) != len(p.Panes) {
		return fmt.Errorf(
			"%w: have %d splits, %d panes",
			ErrSplitsMismatch, len(p.Splits), len(p.Panes),
		)
	}
	for i := range p.Panes {
		if err := p.Panes[i].validateShape(); err != nil {
			return fmt.Errorf("panes[%d]: %w", i, err)
		}
	}
	return nil
}

// walkLeaves invokes f on each leaf in document order, stopping at the first
// error. Containers are descended into; non-leaf, non-container panes are
// rejected upstream by validateShape, so we treat them as no-ops here.
func walkLeaves(p PaneSpec, f func(PaneSpec) error) error {
	if p.IsLeaf() {
		return f(p)
	}
	for i := range p.Panes {
		if err := walkLeaves(p.Panes[i], f); err != nil {
			return err
		}
	}
	return nil
}

package muxctl

import "fmt"

// Validate checks layout and pane-tree shape, returning the first error with
// positional context. It does not contact a driver or run leaf commands.
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

func (p PaneSpec) validateShape() error {
	leaf := p.IsLeaf()
	cont := p.IsContainer()
	if leaf == cont {
		return ErrLeafXorContainer
	}
	if leaf {
		if p.Name == "" {
			return ErrLeafNameRequired
		}
		return nil
	}
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

// walkLeaves calls f for every leaf in document order, stopping and returning
// the first error.
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

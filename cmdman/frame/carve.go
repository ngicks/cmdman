package frame

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// ComponentArgv resolves a built-in component name (see [BuiltinComponents]) to
// the argv that runs it. [Spec.Carve] calls it once per component entry; a def
// made only of command: entries never needs one.
type ComponentArgv func(component string) ([]string, error)

// restSize is the size given to the leftover sibling at every carving level: a
// bare proportional weight, so [muxctl.ComputeChildCells] reserves the entry's
// cells or percentage first and hands the rest everything that is left.
var restSize = muxctl.Size{N: 1}

// EntryPaneName returns the muxctl leaf name carrying the i-th entry of a def.
func EntryPaneName(i int) string {
	return "frame-" + strconv.Itoa(i)
}

// Carve maps the def's entries onto a [muxctl.PaneSpec] tree with main at the
// innermost leftover position.
//
// Entry i becomes a two-child container holding the entry's pane at its edge
// (sized by the entry) and the carve of entries i+1.. at the other side (a bare
// weight, so it absorbs whatever is left). Recursion bottoms out at main, which
// may itself be a leaf or a whole layout tree. This is what makes the flat array
// express nesting: each entry only ever divides the rectangle its predecessors
// left over, which is also what makes a percentage resolve against the remaining
// rectangle (D30) — muxctl resolves a percent against its container's dimension,
// and that container is the remainder by construction.
//
// The result is a pane tree, not a layout; callers wrap it in a
// [muxctl.Layout] and validate it there.
func (s Spec) Carve(main muxctl.PaneSpec, component ComponentArgv) (muxctl.PaneSpec, error) {
	return carve(s.Entries, 0, main, component)
}

func carve(
	entries []Entry,
	i int,
	main muxctl.PaneSpec,
	component ComponentArgv,
) (muxctl.PaneSpec, error) {
	if len(entries) == 0 {
		return main, nil
	}

	entry := entries[0]
	argv, err := entryArgv(entry, component)
	if err != nil {
		return muxctl.PaneSpec{}, fmt.Errorf("frame: entry %d: %w", i, err)
	}
	rest, err := carve(entries[1:], i+1, main, component)
	if err != nil {
		return muxctl.PaneSpec{}, err
	}

	pane := muxctl.PaneSpec{
		Leaf: muxctl.Leaf{Name: EntryPaneName(i), Cmd: argv},
	}

	dir := muxctl.DirHorizontal
	if entry.Edge == EdgeTop || entry.Edge == EdgeBottom {
		dir = muxctl.DirVertical
	}
	// Document order is left-to-right / top-to-bottom, so a top or left entry
	// leads and a bottom or right entry trails the leftover.
	panes := []muxctl.PaneSpec{pane, rest}
	splits := []muxctl.Size{entry.Size, restSize}
	if entry.Edge == EdgeBottom || entry.Edge == EdgeRight {
		panes = []muxctl.PaneSpec{rest, pane}
		splits = []muxctl.Size{restSize, entry.Size}
	}

	return muxctl.PaneSpec{
		Container: muxctl.Container{Dir: dir, Splits: splits, Panes: panes},
	}, nil
}

func entryArgv(entry Entry, component ComponentArgv) ([]string, error) {
	if entry.Component == "" {
		if len(entry.Command) == 0 {
			return nil, errors.New("neither component nor command is set")
		}
		return slices.Clone(entry.Command), nil
	}
	if component == nil {
		return nil, fmt.Errorf("component %q: no component resolver supplied", entry.Component)
	}
	argv, err := component(entry.Component)
	if err != nil {
		return nil, fmt.Errorf("component %q: %w", entry.Component, err)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("component %q: resolver returned empty argv", entry.Component)
	}
	return argv, nil
}

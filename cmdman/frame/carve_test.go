package frame_test

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/cmdman/frame"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// widgetArgv stands in for the widget entrypoint the frame verbs will supply.
func widgetArgv(component string) ([]string, error) {
	return []string{"cmdman", "tui", "widget", component}, nil
}

// mainPane is the caller-provided main region: whatever renders in what the
// frame leaves over.
var mainPane = muxctl.PaneSpec{
	Leaf: muxctl.Leaf{Name: "main", Cmd: []string{"sh"}},
}

type rect struct{ w, h int }

// leafRects resolves a carved tree to per-leaf cell rectangles the way the tmux
// driver's materialize does (muxctl/tmux/apply.go): ComputeChildCells per
// container, ChildDims per child. Testing the resolved geometry — not the tree
// shape — is what distinguishes carving orders, since the two orders produce the
// same Splits values and differ only in nesting.
func leafRects(t *testing.T, node muxctl.PaneSpec, w, h int, out map[string]rect) {
	t.Helper()
	if node.IsLeaf() {
		out[node.Name] = rect{w: w, h: h}
		return
	}
	cells := muxctl.ComputeChildCells(muxctl.ParentDim(node.Dir, w, h), node.Splits)
	for i, child := range node.Panes {
		cw, ch := muxctl.ChildDims(node.Dir, w, h, cells[i])
		leafRects(t, child, cw, ch, out)
	}
}

func carveRects(t *testing.T, content string, w, h int) map[string]rect {
	t.Helper()
	spec, err := normalize(t, content)
	assert.NilError(t, err)
	root, err := spec.Carve(mainPane, widgetArgv)
	assert.NilError(t, err)

	// The carved tree must be a shape the driver accepts.
	assert.NilError(t, muxctl.MuxSpec{
		Layouts: []muxctl.Layout{{Name: "frame", Root: root}},
	}.Validate())

	out := make(map[string]rect)
	leafRects(t, root, w, h, out)
	return out
}

const (
	sideThenBar = `
frame:
  - edge: left
    size: 20%
    component: switcher
  - edge: bottom
    size: 2
    component: statusbar
`
	barThenSide = `
frame:
  - edge: bottom
    size: 2
    component: statusbar
  - edge: left
    size: 20%
    component: switcher
`
)

// Order is the nesting: the same two entries in the other order give a
// different tree, so the side column runs full height in one and the status bar
// runs full width in the other.
func TestCarve_OrderDependence(t *testing.T) {
	// frame-0 is the first entry, frame-1 the second.
	side := carveRects(t, sideThenBar, 200, 50)
	assert.Equal(t, side["frame-0"], rect{w: 40, h: 50}, "switcher spans the full height")
	assert.Equal(t, side["frame-1"], rect{w: 159, h: 2}, "status bar spans only the remainder")
	assert.Equal(t, side["main"], rect{w: 159, h: 47})

	bar := carveRects(t, barThenSide, 200, 50)
	assert.Equal(t, bar["frame-0"], rect{w: 200, h: 2}, "status bar spans the full width")
	assert.Equal(t, bar["frame-1"], rect{w: 40, h: 47}, "switcher is shortened by the bar")
	assert.Equal(t, bar["main"], rect{w: 159, h: 47})
}

// A percentage resolves against the rectangle remaining at that entry's turn
// (D30), not the whole screen: the second 50% of a 200-cell width is 49, not
// 100.
func TestCarve_PercentOfRemainder(t *testing.T) {
	got := carveRects(t, `
frame:
  - edge: left
    size: 50%
    component: switcher
  - edge: left
    size: 50%
    command: [btop]
`, 200, 50)

	assert.Equal(t, got["frame-0"].w, 100)
	assert.Equal(t, got["frame-1"].w, 49)
	assert.Equal(t, got["main"].w, 49)
}

func TestCarve_Edges(t *testing.T) {
	for _, tc := range []struct {
		edge string
		want rect
		dir  muxctl.Direction
		// first reports whether the entry pane leads its container in document
		// order (left/top) or trails it (right/bottom).
		first bool
	}{
		{edge: "left", want: rect{w: 10, h: 50}, dir: muxctl.DirHorizontal, first: true},
		{edge: "right", want: rect{w: 10, h: 50}, dir: muxctl.DirHorizontal, first: false},
		{edge: "top", want: rect{w: 200, h: 10}, dir: muxctl.DirVertical, first: true},
		{edge: "bottom", want: rect{w: 200, h: 10}, dir: muxctl.DirVertical, first: false},
	} {
		t.Run(tc.edge, func(t *testing.T) {
			content := "frame:\n  - edge: " + tc.edge + "\n    size: 10\n    component: switcher\n"
			spec, err := normalize(t, content)
			assert.NilError(t, err)
			root, err := spec.Carve(mainPane, widgetArgv)
			assert.NilError(t, err)

			assert.Equal(t, root.Dir, tc.dir)
			assert.Equal(t, len(root.Panes), 2)
			entryIdx := 1
			if tc.first {
				entryIdx = 0
			}
			assert.Equal(t, root.Panes[entryIdx].Name, "frame-0")
			assert.Equal(t, root.Panes[1-entryIdx].Name, "main")
			// The entry keeps its own size; the leftover sibling is a bare
			// weight that absorbs the rest.
			assert.Equal(t, root.Splits[entryIdx], muxctl.Size{N: 10, Absolute: true})
			assert.Equal(t, root.Splits[1-entryIdx], muxctl.Size{N: 1})

			out := make(map[string]rect)
			leafRects(t, root, 200, 50, out)
			assert.Equal(t, out["frame-0"], tc.want)
		})
	}
}

func TestCarve_Argv(t *testing.T) {
	spec, err := normalize(t, `
frame:
  - edge: left
    size: 20%
    component: switcher
  - edge: right
    size: 30%
    command: ["btop", "--utf-force"]
`)
	assert.NilError(t, err)
	root, err := spec.Carve(mainPane, widgetArgv)
	assert.NilError(t, err)

	cmds := make(map[string][]string)
	var walk func(muxctl.PaneSpec)
	walk = func(p muxctl.PaneSpec) {
		if p.IsLeaf() {
			cmds[p.Name] = p.Cmd
			return
		}
		for _, c := range p.Panes {
			walk(c)
		}
	}
	walk(root)

	assert.DeepEqual(t, cmds["frame-0"], []string{"cmdman", "tui", "widget", "switcher"})
	// command: argv passes through verbatim.
	assert.DeepEqual(t, cmds["frame-1"], []string{"btop", "--utf-force"})
	assert.DeepEqual(t, cmds["main"], []string{"sh"})
}

// Carve is total on an empty def so the recursion's base case needs no special
// casing at the call site; Normalize is where an empty def is rejected.
func TestCarve_NoEntries(t *testing.T) {
	root, err := frame.Spec{}.Carve(mainPane, nil)
	assert.NilError(t, err)
	assert.DeepEqual(t, root, mainPane)
}

func TestCarve_ComponentResolver(t *testing.T) {
	spec, err := normalize(t, "frame:\n  - edge: left\n    size: 10\n    component: switcher\n")
	assert.NilError(t, err)

	t.Run("nil resolver", func(t *testing.T) {
		_, err := spec.Carve(mainPane, nil)
		assert.Assert(t, err != nil)
		assert.Assert(t, cmp.Contains(err.Error(), "no component resolver supplied"))
	})

	t.Run("empty argv", func(t *testing.T) {
		_, err := spec.Carve(mainPane, func(string) ([]string, error) { return nil, nil })
		assert.Assert(t, err != nil)
		assert.Assert(t, cmp.Contains(err.Error(), "empty argv"))
	})

	t.Run("command-only def needs no resolver", func(t *testing.T) {
		cmdOnly, err := normalize(t,
			"frame:\n  - edge: left\n    size: 10\n    command: [btop]\n")
		assert.NilError(t, err)
		_, err = cmdOnly.Carve(mainPane, nil)
		assert.NilError(t, err)
	})
}

func TestCarve_NestedMainRegion(t *testing.T) {
	spec, err := normalize(t, sideThenBar)
	assert.NilError(t, err)

	project := muxctl.PaneSpec{
		Container: muxctl.Container{
			Dir:    muxctl.DirVertical,
			Splits: []muxctl.Size{{N: 1}, {N: 1}},
			Panes: []muxctl.PaneSpec{
				{Leaf: muxctl.Leaf{Name: "api", Cmd: []string{"sh"}}},
				{Leaf: muxctl.Leaf{Name: "web", Cmd: []string{"sh"}}},
			},
		},
	}
	root, err := spec.Carve(project, widgetArgv)
	assert.NilError(t, err)
	assert.NilError(t, muxctl.MuxSpec{
		Layouts: []muxctl.Layout{{Name: "frame", Root: root}},
	}.Validate())

	names := muxctl.AppendLeafNames(nil, root)
	assert.Assert(t, cmp.Contains(strings.Join(names, ","), "api,web"))

	out := make(map[string]rect)
	leafRects(t, root, 200, 50, out)
	// The project layout divides the main region, not the whole window.
	assert.Equal(t, out["api"], rect{w: 159, h: 23})
	assert.Equal(t, out["web"], rect{w: 159, h: 23})
}

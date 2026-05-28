package muxctl_test

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// Constructors that keep the validation table compact and readable.

func leaf(name string, focus ...bool) muxctl.PaneSpec {
	p := muxctl.PaneSpec{Leaf: muxctl.Leaf{Name: name, Cmd: []string{"./" + name}}}
	if len(focus) > 0 && focus[0] {
		p.Focus = true
	}
	return p
}

func hContainer(children ...muxctl.PaneSpec) muxctl.PaneSpec {
	return container(muxctl.DirHorizontal, children...)
}

func vContainer(children ...muxctl.PaneSpec) muxctl.PaneSpec {
	return container(muxctl.DirVertical, children...)
}

func container(dir muxctl.Direction, children ...muxctl.PaneSpec) muxctl.PaneSpec {
	splits := make([]muxctl.Size, len(children))
	for i := range splits {
		splits[i] = muxctl.Size{N: 1}
	}
	return muxctl.PaneSpec{
		Container: muxctl.Container{Dir: dir, Splits: splits, Panes: children},
	}
}

func TestMuxSpec_Validate_Ok(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec muxctl.MuxSpec
	}{
		{
			name: "single leaf root",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{Name: "l1", Root: leaf("a")}}},
		},
		{
			name: "h-container with leaves",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: hContainer(leaf("a"), leaf("b")),
			}}},
		},
		{
			name: "nested containers, unique names, one focus",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: hContainer(
					leaf("api"),
					leaf("worker"),
					vContainer(leaf("redis"), leaf("db", true)),
				),
			}}},
		},
		{
			name: "multiple layouts, names unique",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{
				{Name: "front", Root: leaf("api")},
				{Name: "back", Root: leaf("worker")},
			}},
		},
		{
			name: "absolute and proportional splits",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: muxctl.PaneSpec{
					Container: muxctl.Container{
						Dir:    muxctl.DirHorizontal,
						Splits: []muxctl.Size{{N: 90, Absolute: true}, {N: 1}, {N: 2}},
						Panes:  []muxctl.PaneSpec{leaf("a"), leaf("b"), leaf("c")},
					},
				},
			}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.NilError(t, tc.spec.Validate())
		})
	}
}

func TestMuxSpec_Validate_Errors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		spec    muxctl.MuxSpec
		wantErr error
	}{
		{
			name:    "layout name missing",
			spec:    muxctl.MuxSpec{Layouts: []muxctl.Layout{{Name: "", Root: leaf("a")}}},
			wantErr: muxctl.ErrLayoutNameRequired,
		},
		{
			name: "duplicate layout names",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{
				{Name: "l1", Root: leaf("a")},
				{Name: "l1", Root: leaf("b")},
			}},
			wantErr: muxctl.ErrDuplicateLayoutName,
		},
		{
			name: "pane is both leaf and container",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: muxctl.PaneSpec{
					Container: muxctl.Container{Dir: muxctl.DirHorizontal},
					Leaf:      muxctl.Leaf{Cmd: []string{"./a"}},
				},
			}}},
			wantErr: muxctl.ErrLeafXorContainer,
		},
		{
			name: "pane is neither leaf nor container",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: muxctl.PaneSpec{},
			}}},
			wantErr: muxctl.ErrLeafXorContainer,
		},
		{
			name: "container invalid direction",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: muxctl.PaneSpec{
					Container: muxctl.Container{
						Dir:    "x",
						Splits: []muxctl.Size{{N: 1}},
						Panes:  []muxctl.PaneSpec{leaf("a")},
					},
				},
			}}},
			wantErr: muxctl.ErrInvalidDirection,
		},
		{
			name: "container empty panes",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: muxctl.PaneSpec{
					Container: muxctl.Container{Dir: muxctl.DirHorizontal},
				},
			}}},
			wantErr: muxctl.ErrEmptyContainer,
		},
		{
			name: "splits length mismatch",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: muxctl.PaneSpec{
					Container: muxctl.Container{
						Dir:    muxctl.DirHorizontal,
						Splits: []muxctl.Size{{N: 1}, {N: 2}},
						Panes:  []muxctl.PaneSpec{leaf("a")},
					},
				},
			}}},
			wantErr: muxctl.ErrSplitsMismatch,
		},
		{
			name: "leaf with empty name",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: muxctl.PaneSpec{Leaf: muxctl.Leaf{Cmd: []string{"./x"}}},
			}}},
			wantErr: muxctl.ErrLeafNameRequired,
		},
		{
			name: "duplicate pane names in layout (siblings)",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: hContainer(leaf("api"), leaf("api")),
			}}},
			wantErr: muxctl.ErrDuplicatePaneName,
		},
		{
			name: "duplicate pane names across nested containers",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: hContainer(
					leaf("api"),
					vContainer(leaf("api"), leaf("db")),
				),
			}}},
			wantErr: muxctl.ErrDuplicatePaneName,
		},
		{
			name: "more than one focus per layout",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: hContainer(leaf("a", true), leaf("b", true)),
			}}},
			wantErr: muxctl.ErrMultipleFocus,
		},
		{
			name: "nested invariant: child container empty",
			spec: muxctl.MuxSpec{Layouts: []muxctl.Layout{{
				Name: "l1",
				Root: muxctl.PaneSpec{
					Container: muxctl.Container{
						Dir:    muxctl.DirHorizontal,
						Splits: []muxctl.Size{{N: 1}, {N: 1}},
						Panes: []muxctl.PaneSpec{
							leaf("a"),
							// empty container
							{Container: muxctl.Container{Dir: muxctl.DirVertical}},
						},
					},
				},
			}}},
			wantErr: muxctl.ErrEmptyContainer,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorIs(t, tc.spec.Validate(), tc.wantErr)
		})
	}
}

// TestMuxSpec_Validate_FocusOk guards against the focus-counter regressing
// to "any > 0 fails" — a single focused leaf must pass.
func TestMuxSpec_Validate_FocusOk(t *testing.T) {
	t.Parallel()

	spec := muxctl.MuxSpec{Layouts: []muxctl.Layout{{
		Name: "l1",
		Root: hContainer(leaf("a"), leaf("b", true), leaf("c")),
	}}}
	assert.NilError(t, spec.Validate())
}

// TestMuxSpec_Validate_FocusScopedPerLayout ensures the focus counter is
// reset per layout — a single focus in each of several layouts is fine.
func TestMuxSpec_Validate_FocusScopedPerLayout(t *testing.T) {
	t.Parallel()

	spec := muxctl.MuxSpec{Layouts: []muxctl.Layout{
		{Name: "l1", Root: hContainer(leaf("a", true), leaf("b"))},
		{Name: "l2", Root: hContainer(leaf("c", true), leaf("d"))},
	}}
	assert.NilError(t, spec.Validate())
}

// TestMuxSpec_Validate_NameUniquenessScopedPerLayout ensures the same pane
// name may appear in two different layouts.
func TestMuxSpec_Validate_NameUniquenessScopedPerLayout(t *testing.T) {
	t.Parallel()

	spec := muxctl.MuxSpec{Layouts: []muxctl.Layout{
		{Name: "l1", Root: leaf("shared")},
		{Name: "l2", Root: leaf("shared")},
	}}
	assert.NilError(t, spec.Validate())
}

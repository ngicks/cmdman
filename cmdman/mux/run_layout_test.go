package mux

import (
	"context"
	"io"
	"slices"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// layoutTestSession names the session the layout tests bring their dashboards
// up in.
const layoutTestSession = "layout"

// layoutsSpec builds a spec of one layout per name, each the two-pane dashboard
// [upSpec] builds but with pane names carrying its own layout's. Which layout a
// [Run] applied is then readable off the window's pane titles, next to the
// marker it left behind.
func layoutsSpec(socket string, names ...string) muxctl.MuxSpec {
	spec := muxctl.MuxSpec{Driver: muxctl.DriverSpec{Name: "tmux", Socket: socket}}
	for _, name := range names {
		spec.Layouts = append(spec.Layouts, muxctl.Layout{
			Name: name,
			Root: muxctl.PaneSpec{Container: muxctl.Container{
				Dir:    muxctl.DirHorizontal,
				Splits: []muxctl.Size{{N: 1}, {N: 1}},
				Panes: []muxctl.PaneSpec{
					sleepLeaf(name + "-web"),
					sleepLeaf(name + "-worker"),
				},
			}},
		})
	}
	return spec
}

// layoutUp runs spec for identity with the given layout selector and
// KeepLayout. The env is empty for the reason [runUp]'s is: outside tmux there
// is no current window to take over, so the window under test is the one Run
// resolved by identity or built for itself.
func layoutUp(t *testing.T, spec muxctl.MuxSpec, identity, layout string, keep bool) {
	t.Helper()
	err := Run(context.Background(), spec, RunOptions{
		SessionName: layoutTestSession,
		WindowName:  "cmdman-keep",
		Identity:    identity,
		Layout:      layout,
		KeepLayout:  keep,
		Env:         []string{},
		Stdout:      io.Discard,
	})
	if err != nil {
		t.Fatalf("Run(Layout %q, KeepLayout %v): %v", layout, keep, err)
	}
}

// assertLayout checks both halves of what a Run decided: the panes say which
// layout the user is looking at, the marker says where the next cycling Run
// would resume from.
func assertLayout(t *testing.T, bin, socket, identity, want string, wantMarker int) {
	t.Helper()
	window := theWindowOf(t, socket, identity)
	if window.Marker != wantMarker {
		t.Errorf("marker = %d, want %d", window.Marker, wantMarker)
	}
	got := projectPaneTitles(t, bin, socket, window.WindowID)
	if wantTitles := []string{want + "-web", want + "-worker"}; !slices.Equal(got, wantTitles) {
		t.Errorf("panes = %v, want layout %q's %v", got, want, wantTitles)
	}
}

// TestRun_KeepLayout covers the bring-up's layout contract: a caller whose
// gesture is "make this project running" hands the user back the dashboard they
// left, rather than the next layout along. Cycling stays what an empty selector
// means without it — that is the CLI's documented `mux up` behavior.
//
// The subtest names stay short: each builds a tmux server on a socket named
// after the test, and the socket path has to fit in sockaddr_un.
func TestRun_KeepLayout(t *testing.T) {
	const identity = "wdhash-keep"

	t.Run("existing window", func(t *testing.T) {
		bin, socket := identitySocket(t)
		spec := layoutsSpec(socket, "one", "two", "three")

		layoutUp(t, spec, identity, "1", false)
		assertLayout(t, bin, socket, identity, "two", 1)

		layoutUp(t, spec, identity, "", true)
		assertLayout(t, bin, socket, identity, "two", 1)
	})

	t.Run("fresh window", func(t *testing.T) {
		bin, socket := identitySocket(t)

		layoutUp(t, layoutsSpec(socket, "one", "two", "three"), identity, "", true)
		assertLayout(t, bin, socket, identity, "one", 0)
	})

	t.Run("explicit wins", func(t *testing.T) {
		bin, socket := identitySocket(t)
		spec := layoutsSpec(socket, "one", "two", "three")

		layoutUp(t, spec, identity, "0", false)
		layoutUp(t, spec, identity, "2", true)
		assertLayout(t, bin, socket, identity, "three", 2)
	})

	t.Run("shrunken spec", func(t *testing.T) {
		bin, socket := identitySocket(t)

		layoutUp(t, layoutsSpec(socket, "one", "two", "three", "four"), identity, "3", false)
		assertLayout(t, bin, socket, identity, "four", 3)

		// The spec has lost layouts since the marker was written, so the layout it
		// records no longer exists. Clamping shows the last one that does, instead
		// of indexing past the end — the marker is two past it here, so wrapping
		// would land on the first layout rather than the nearest kept one.
		layoutUp(t, layoutsSpec(socket, "one", "two"), identity, "", true)
		assertLayout(t, bin, socket, identity, "two", 1)
	})

	t.Run("still cycles", func(t *testing.T) {
		bin, socket := identitySocket(t)
		spec := layoutsSpec(socket, "one", "two", "three")

		layoutUp(t, spec, identity, "", false)
		assertLayout(t, bin, socket, identity, "one", 0)

		layoutUp(t, spec, identity, "", false)
		assertLayout(t, bin, socket, identity, "two", 1)
	})
}

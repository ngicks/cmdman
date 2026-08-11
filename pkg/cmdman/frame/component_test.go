package frame_test

import (
	"slices"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/pkg/cmdman/frame"
	"github.com/ngicks/cmdman/pkg/cmdman/tui"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

func TestWidgetArgv(t *testing.T) {
	resolve := frame.WidgetArgv("/opt/bin/cmdman")
	argv, err := resolve(frame.ComponentSwitcher)
	assert.NilError(t, err)
	assert.DeepEqual(t, argv, []string{"/opt/bin/cmdman", "tui", "widget", "switcher"})

	argv, err = frame.WidgetArgv("")(frame.ComponentStatusbar)
	assert.NilError(t, err)
	assert.DeepEqual(t, argv, []string{"cmdman", "tui", "widget", "statusbar"})

	_, err = resolve("nope")
	assert.ErrorContains(t, err, "unknown component")
}

// TestWidgetArgvCarves checks the resolver through the seam it exists for: a
// def naming a component carries the widget invocation into the pane tree.
func TestWidgetArgvCarves(t *testing.T) {
	spec := frame.Spec{Entries: []frame.Entry{{
		Edge:      frame.EdgeLeft,
		Size:      muxctl.Size{N: 20, Percent: true},
		Component: frame.ComponentSwitcher,
	}}}
	pane, err := spec.Carve(mainPane, frame.WidgetArgv("cmdman"))
	assert.NilError(t, err)
	assert.DeepEqual(t,
		pane.Panes[0].Cmd,
		[]string{"cmdman", "tui", "widget", "switcher"})
}

// TestBuiltinComponentsMatchWidgets pins the direction of the contract that
// matters: every built-in component name a def may reference must be a widget
// the entrypoint can actually run. The converse does not hold — a widget need
// not be dockable chrome (the launcher is a popup selector, D16 names only the
// switcher and the statusbar as built-in components).
func TestBuiltinComponentsMatchWidgets(t *testing.T) {
	widgets := tui.WidgetKeys()
	slices.Sort(widgets)
	for _, component := range frame.BuiltinComponents() {
		assert.Check(t, cmp.Contains(widgets, component))
	}
}

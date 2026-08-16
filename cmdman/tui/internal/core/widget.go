package core

import (
	"fmt"
	"strings"
)

// Widget names a single-view widget mode: one view filling the pane, with no
// tab bar and no tab switching. Widgets are the entry point a frame def's
// `component:` resolves to (`cmdman tui widget <name>`), and each one is its own
// CLI subcommand.
type Widget int

const (
	// WidgetNone is the zero value and names no widget: the full TUI runs.
	WidgetNone Widget = iota
	// WidgetSwitcher is the docked project switcher: projects with their
	// commands listed under them.
	WidgetSwitcher
	// WidgetLauncher is the quick-launch selector: locations left, their compose
	// projects right. It is the view a mux key binding summons as a popup (D3).
	WidgetLauncher
	// WidgetProjectManager is the project-manager shortcut panel: scale,
	// shown-replica cycling, and layout cycling for one project. Popup-
	// summoned like the launcher; never a frame component (D6).
	WidgetProjectManager
)

// WidgetDef is one row of WidgetDefs.
type WidgetDef struct {
	Widget Widget
	Key    string
}

// WidgetDefs is the single source of truth for the widget modes and their CLI
// tokens — the `cmdman tui widget <name>` subcommand names, which are also the
// built-in component names a frame def references. WidgetNone is deliberately
// absent: it names no widget.
var WidgetDefs = []WidgetDef{
	{WidgetSwitcher, "switcher"},
	{WidgetLauncher, "launcher"},
	{WidgetProjectManager, "project-manager"},
}

// WidgetKeys returns the widget CLI tokens in declaration order.
func WidgetKeys() []string {
	keys := make([]string, len(WidgetDefs))
	for i, d := range WidgetDefs {
		keys[i] = d.Key
	}
	return keys
}

// ParseWidget maps a widget CLI token to its Widget. It is the inverse of the
// WidgetDefs key column; WidgetNone has no token and never parses.
func ParseWidget(s string) (Widget, error) {
	for _, d := range WidgetDefs {
		if d.Key == s {
			return d.Widget, nil
		}
	}
	return WidgetNone, fmt.Errorf(
		"invalid widget %q: want one of %s", s, strings.Join(WidgetKeys(), ", "))
}

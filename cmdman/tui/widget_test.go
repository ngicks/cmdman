package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"
	"github.com/ngicks/cmdman/cmdman/tui/widget/launcher"
	"github.com/ngicks/cmdman/cmdman/tui/widget/switcher"
)

// TestWidgetDefsInSync guards the invariant that WidgetKeys and ParseWidget both
// derive from core.WidgetDefs, so the `tui widget <name>` subcommands and the frame
// component names can never drift. WidgetNone names no widget and must not
// parse.
func TestWidgetDefsInSync(t *testing.T) {
	keys := WidgetKeys()
	if len(keys) != len(core.WidgetDefs) {
		t.Fatalf("WidgetKeys() len = %d, want %d", len(keys), len(core.WidgetDefs))
	}
	for i, d := range core.WidgetDefs {
		if keys[i] != d.Key {
			t.Errorf("WidgetKeys()[%d] = %q, want %q", i, keys[i], d.Key)
		}
		got, err := ParseWidget(d.Key)
		if err != nil {
			t.Errorf("ParseWidget(%q) unexpected error: %v", d.Key, err)
		}
		if got != d.Widget {
			t.Errorf("ParseWidget(%q) = %d, want %d", d.Key, got, d.Widget)
		}
		if d.Widget == core.WidgetNone {
			t.Errorf("core.WidgetDefs[%d] maps %q to WidgetNone", i, d.Key)
		}
	}
	if _, err := ParseWidget(""); err == nil {
		t.Errorf("ParseWidget(%q) should return an error", "")
	}
	if _, err := ParseWidget("nope"); err == nil {
		t.Errorf("ParseWidget(%q) should return an error", "nope")
	}
}

// TestWidgetOptionSelectsModel covers the widget-mode option itself: naming a
// widget runs that widget's own model, and leaving it unset (the zero value
// every existing caller passes) still runs the full multi-tab model.
func TestWidgetOptionSelectsModel(t *testing.T) {
	ctx := context.Background()
	fb := &coretest.FakeBackend{}

	full := newProgramModel(ctx, core.Options{Backend: fb, InitialTab: TabCompose})
	m, ok := full.(Model)
	if !ok {
		t.Fatalf("Widget unset should run the full model, got %T", full)
	}
	if m.active != TabCompose {
		t.Errorf("full model active tab = %d, want %d", m.active, TabCompose)
	}

	got := newProgramModel(ctx, core.Options{Backend: fb, Widget: core.WidgetLauncher})
	if _, ok := got.(launcher.Model); !ok {
		t.Errorf("WidgetLauncher should run the launcher model, got %T", got)
	}

	got = newProgramModel(ctx, core.Options{Backend: fb, Widget: core.WidgetSwitcher})
	if _, ok := got.(switcher.Model); !ok {
		t.Fatalf("WidgetSwitcher should run the switcher model, got %T", got)
	}
	// Clicking a project is one of the switcher's two selection gestures (D24),
	// so the docked column asks for the mouse.
	if mode := got.View().MouseMode; mode != tea.MouseModeCellMotion {
		t.Errorf("the switcher renders MouseMode %v, want %v", mode, tea.MouseModeCellMotion)
	}
}

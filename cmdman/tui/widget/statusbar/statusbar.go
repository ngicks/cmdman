// Package statusbar implements the one-line status bar: where you are, then
// what is running, then what this is — the working directory's project with its
// marker, the counts across every project, and the version at the right edge.
//
// It is a facade over cmdman/tui/widget/internal/panel. The statusbar and the
// switcher are one model by design — they read the same two listings over the
// same event subscription and share the update loop; the switcher's selection
// and mouse handling simply stays dormant here — so what a widget package adds
// is the name, not an implementation.
package statusbar

import (
	"context"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
	"github.com/ngicks/cmdman/cmdman/tui/widget/internal/panel"
)

// Model is the statusbar widget. It is the panel model itself, aliased rather
// than wrapped: a wrapper would dissolve into the panel on the first Update,
// advertising a type the program stops holding after one message.
type Model = panel.Model

// New constructs the statusbar from the same options the full model takes.
func New(ctx context.Context, opts core.Options) Model {
	return panel.New(ctx, core.WidgetStatusbar, opts)
}

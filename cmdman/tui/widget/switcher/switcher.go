// Package switcher implements the docked project switcher: every project
// heading its group with one marker slot, its commands listed under it, and
// enter or a click taking the client to that project's window (D6, D24).
//
// It is a facade over cmdman/tui/widget/internal/panel. The switcher and the
// statusbar are one model by design — they read the same two listings over the
// same event subscription and share the update loop and the mouse geometry, and
// differ only in the renderer — so what a widget package adds is the name, not
// an implementation.
package switcher

import (
	"context"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
	"github.com/ngicks/cmdman/cmdman/tui/widget/internal/panel"
)

// Model is the switcher widget. It is the panel model itself, aliased rather
// than wrapped: a wrapper would dissolve into the panel on the first Update,
// advertising a type the program stops holding after one message.
type Model = panel.Model

// New constructs the switcher from the same options the full model takes.
func New(ctx context.Context, opts core.Options) Model {
	return panel.New(ctx, core.WidgetSwitcher, opts)
}

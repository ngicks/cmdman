// Package tui implements the interactive terminal UI for `cmdman tui`.
//
// The TUI is a multi-tab browser over compose-managed commands. It uses
// bubbletea as the renderer. Unlike cmdman/cli/progress_tty.go (which
// deliberately avoids a full TUI framework because framework startup queries
// the terminal for the whole binary and can corrupt the PTY of sibling
// subcommands such as `compose attach`), the tui subcommand is its own
// standalone process and does not spawn sibling subcommands that share its
// PTY, so the framework-startup concern does not apply here. Attach is handled
// through an explicit terminal handoff (see attach handling in the runtime
// layer), not by spawning a sibling command under the TUI's PTY.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
	"github.com/ngicks/cmdman/cmdman/tui/widget/launcher"
	"github.com/ngicks/cmdman/cmdman/tui/widget/statusbar"
	"github.com/ngicks/cmdman/cmdman/tui/widget/switcher"
)

// Run starts the TUI program and blocks until it exits.
func Run(ctx context.Context, opts Options) error {
	// v2: the alternate screen is requested per-frame via View().AltScreen
	// (see Model.View), not as a program option.
	p := tea.NewProgram(newProgramModel(ctx, opts), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// newProgramModel picks the model Run drives: the one the widget package named
// by Options.Widget builds, the full multi-tab model otherwise. The switcher
// and the statusbar are two names for one panel model — they share the update
// loop and differ only in the renderer — while the launcher is a model of its
// own: its keys are zoned (a bare letter types in the input and acts on a
// list), so it shares no key handling with the docked widgets.
func newProgramModel(ctx context.Context, opts Options) tea.Model {
	switch opts.Widget {
	case core.WidgetSwitcher:
		return switcher.New(ctx, opts)
	case core.WidgetStatusbar:
		return statusbar.New(ctx, opts)
	case core.WidgetLauncher:
		return launcher.New(ctx, opts)
	}
	m := New(opts)
	m.ctx = ctx
	return m
}

// New constructs the root model.
func New(opts Options) Model {
	return Model{
		backend:   opts.Backend,
		version:   opts.Version,
		popupMode: opts.PopupMode,
		altScreen: opts.AltScreen,
		active:    opts.InitialTab,
		commands: commandsTab{
			fold:  map[string]bool{},
			focus: paneList,
		},
		// -1 = no dashboard marker known until the first ListLayouts load.
		layout: layoutTab{current: -1},
	}
}

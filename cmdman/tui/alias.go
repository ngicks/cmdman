package tui

import "github.com/ngicks/cmdman/cmdman/tui/internal/core"

// The TUI's data contract, tabs and widget modes live in internal/core so a
// widget can be its own package without importing the model that hosts it.
// They are re-exported here because they are this package's public API: the
// CLI, the frame layer and the widget subcommands name them as tui.X.

type (
	Backend      = core.Backend
	CommandInfo  = core.CommandInfo
	ProjectInfo  = core.ProjectInfo
	LayoutsInfo  = core.LayoutsInfo
	SwitchTarget = core.SwitchTarget
	Options      = core.Options

	ProjectManagerInfo = core.ProjectManagerInfo
	ServiceScaleInfo   = core.ServiceScaleInfo
	DownSummary        = core.DownSummary

	EventSignal        = core.EventSignal
	EventStream        = core.EventStream
	RuntimeStateView   = core.RuntimeStateView
	RuntimeStateUpdate = core.RuntimeStateUpdate
	RuntimeStateStream = core.RuntimeStateStream
	LogLine            = core.LogLine
	LogStream          = core.LogStream
	RawChunk           = core.RawChunk
	RawSize            = core.RawSize
	RawStream          = core.RawStream
	ComposeUpEvent     = core.ComposeUpEvent
	ComposeUpStream    = core.ComposeUpStream

	LaunchTarget   = core.LaunchTarget
	LaunchProject  = core.LaunchProject
	LaunchLocation = core.LaunchLocation
	LaunchOutcome  = core.LaunchOutcome
)

// Attach outcomes.
const (
	AttachDetached = core.AttachDetached
	AttachExited   = core.AttachExited
)

// Tab identifies a top-level tab. It is exported so cmd/cli can name a tab for
// Options.InitialTab and the --tab flag.
type Tab = core.Tab

const (
	TabCommands = core.TabCommands
	TabCompose  = core.TabCompose
)

// TabNames returns the tab display names in tab order (used by the tab bar).
func TabNames() []string { return core.TabNames() }

// TabKeys returns the --tab CLI tokens in tab order.
func TabKeys() []string { return core.TabKeys() }

// ParseTab maps a --tab CLI token to its Tab.
func ParseTab(s string) (Tab, error) { return core.ParseTab(s) }

// NumTabs returns the number of top-level tabs.
func NumTabs() int { return core.NumTabs() }

// Widget names a single-view widget mode: one view filling the pane, with no
// tab bar and no tab switching. Widgets are the entry point a frame def's
// `component:` resolves to (`cmdman tui widget <name>`), and each one is its own
// CLI subcommand.
type Widget = core.Widget

const (
	WidgetNone           = core.WidgetNone
	WidgetSwitcher       = core.WidgetSwitcher
	WidgetLauncher       = core.WidgetLauncher
	WidgetProjectManager = core.WidgetProjectManager
)

// WidgetKeys returns the widget CLI tokens in declaration order.
func WidgetKeys() []string { return core.WidgetKeys() }

// ParseWidget maps a widget CLI token to its Widget. WidgetNone has no token
// and never parses.
func ParseWidget(s string) (Widget, error) { return core.ParseWidget(s) }

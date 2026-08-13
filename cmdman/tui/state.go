package tui

import (
	"cmp"
	"context"
	"slices"

	"github.com/charmbracelet/x/vt"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// pane identifies the focused pane within the Commands tab.
type pane int

const (
	paneList pane = iota
	panePreview
)

// projectMarker is the glyph used for compose project/app rows.
const projectMarker = "⿻"

// Model is the root bubbletea model. Update returns it by value; the embedded
// maps (fold state) are shared by reference, which is intentional so fold
// edits survive the value copy.
type Model struct {
	backend core.Backend
	version string

	// ctx is the program-scoped context used to spawn background readers and
	// service calls from bubbletea commands. It is set by Run; tests that drive
	// Update directly may leave it nil.
	ctx context.Context

	width, height int

	active core.Tab

	commands commandsTab
	compose  composeTab
	layout   layoutTab

	popup     popupState
	helpOpen  bool
	defViewer defViewerState
	composeUp composeUpState

	status string // transient status/error message in the footer
	cwd    string // normalized working directory for active detection

	events    core.EventStream // lifecycle change-signal subscription
	reloadGen int              // debounce generation for event-triggered re-list

	previewGen int // monotonic generation for terminal-preview drain/tick loops

	// termPreviewDisabled turns off the vt terminal-view preview for the rest of
	// the session after the emulator panics on a command's output (the vt/
	// ultraviolet library can panic on some control sequences). Once set, all
	// previews use the crash-proof sanitized-log view instead.
	termPreviewDisabled bool

	popupMode bool // running inside a multiplexer popup
	altScreen bool // render in the alternate screen buffer (set per-View in v2)

	spinner  int  // animation frame for in-progress status markers
	spinning bool // whether the spinner ticker is currently running

	quitting bool
}

type commandsTab struct {
	groups    []core.ProjectGroup
	filter    string
	filtering bool // filter input is focused
	selected  int  // index into visibleRows()
	fold      map[string]bool
	focus     pane
	preview   previewState
}

type composeTab struct {
	rows      []composeRow
	filter    string
	filtering bool
	selected  int
}

type composeRow struct {
	name     string
	path     string
	workdir  string
	commands int
	running  int
	exited   int
	failed   int
	active   bool
	hasMux   bool
	modified string
}

// layoutTab holds the Layout-tab state: the current project's mux layouts in
// definition order, the selected row, and the running dashboard's current
// marker. project/path are the resolved current project (cwd-active mux project,
// falling back to the Compose-tab selection) used when applying a layout.
type layoutTab struct {
	rows     []layoutRow
	selected int
	project  string // resolved current project name
	path     string // resolved compose file path (used to apply a layout)
	current  int    // current dashboard marker index, or -1 when none/unknown
	loaded   bool   // whether layouts have been loaded at least once
}

type layoutRow struct {
	name string
}

// moveSelection applies delta and clamps the layout selection to existing rows.
func (t *layoutTab) moveSelection(delta int) {
	if len(t.rows) == 0 {
		return
	}
	t.selected += delta
	if t.selected < 0 {
		t.selected = 0
	}
	if t.selected >= len(t.rows) {
		t.selected = len(t.rows) - 1
	}
}

// previewState holds the right-pane preview content for the selected command.
//
// A command renders in one of two modes. The default is the sanitized log text
// (lines, fed by stream). A running, TTY-backed command instead renders in
// terminal-view mode (terminal), where raw attach bytes (raw) drive a persistent
// vt emulator (term) sized to the command's reported PTY size.
type previewState struct {
	cmdID  string
	lines  []string
	status previewStatus
	errMsg string
	stream core.LogStream // live Tail+Follow reader for cmdID; nil when none

	terminal  bool             // terminal-view mode (vt emulator) is active
	streaming bool             // raw drain is live; the repaint tick runs while true
	gen       int              // generation of the active drain/tick loop (see Model.previewGen)
	raw       core.RawStream   // live raw attach stream for cmdID; nil when none
	term      *vt.SafeEmulator // vt emulator for terminal-view; nil when none
}

// defViewerState holds the read-only definition-viewer overlay (Compose tab
// `enter`). It shows the project's raw compose YAML file, scrollable with
// j/k/PgUp/PgDn; open reports whether the overlay is shown.
type defViewerState struct {
	open    bool
	project string
	lines   []string
	scroll  int // index of the first visible line
	loading bool
	errMsg  string
}

// composeUpState holds the live compose-up progress overlay (Compose tab `a`).
// While `compose up` runs it shows a per-service mark for each command; on the
// operation's terminal phase the overlay collapses to a footer summary. active
// reports whether the overlay is shown.
type composeUpState struct {
	active  bool
	project string
	order   []string                 // services in first-seen order
	marks   map[string]composeUpMark // service name → latest mark
	stream  core.ComposeUpStream     // event source; nil when none
}

// composeUpMark is the latest known phase for a single service in the overlay.
type composeUpMark struct {
	phase    string
	terminal bool
	failed   bool
}

// anyPending reports whether the overlay is showing work still in flight, so the
// spinner keeps animating until the operation's terminal phase.
func (s *composeUpState) anyPending() bool {
	if !s.active {
		return false
	}
	if len(s.order) == 0 {
		return true // opened but no event yet; show motion while we wait
	}
	for _, name := range s.order {
		if !s.marks[name].terminal {
			return true
		}
	}
	return false
}

type previewStatus int

const (
	previewEmpty previewStatus = iota // "No output yet."
	previewOK
	previewNoStorage // none log driver
	previewError
	previewLoading
)

// visRowKind distinguishes project header rows from command rows.
type visRowKind int

const (
	visProject visRowKind = iota
	visCommand
)

// visRow is a flattened, currently-visible row in the Commands tab.
type visRow struct {
	kind  visRowKind
	group int // index into commandsTab.groups
	cmd   int // index into group.commands (visCommand only)
}

// folded reports the fold state for group index gi; invalid indexes are unfolded.
func (c *commandsTab) folded(gi int) bool {
	if gi < 0 || gi >= len(c.groups) {
		return false
	}
	return c.fold[c.groups[gi].Key()]
}

// setFolded sets group index gi to v; invalid indexes are ignored.
func (c *commandsTab) setFolded(gi int, v bool) {
	if gi < 0 || gi >= len(c.groups) {
		return
	}
	c.fold[c.groups[gi].Key()] = v
}

// visibleRows computes the flattened visible rows honoring the filter and fold
// state. While a filter is active, fold is ignored so matches are reachable.
func (c *commandsTab) visibleRows() []visRow {
	var rows []visRow
	filtering := c.filter != ""
	for gi := range c.groups {
		g := &c.groups[gi]
		projMatch := filtering && core.MatchesFilter(c.filter, g.Name)
		var matched []int
		for ci := range g.Commands {
			if !filtering || projMatch || commandMatches(c.filter, g.Commands[ci]) {
				matched = append(matched, ci)
			}
		}
		if filtering && !projMatch && len(matched) == 0 {
			continue
		}
		// Standalone commands carry no compose project name; list them directly
		// without a (foldable) group header.
		if g.Name != "" {
			rows = append(rows, visRow{kind: visProject, group: gi})
			// When filtering, force-expand so matches are visible; otherwise honor fold.
			if !filtering && c.folded(gi) {
				continue
			}
		}
		for _, ci := range matched {
			rows = append(rows, visRow{kind: visCommand, group: gi, cmd: ci})
		}
	}
	return rows
}

func (c *commandsTab) clampSelection() {
	rows := c.visibleRows()
	if len(rows) == 0 {
		c.selected = 0
		return
	}
	if c.selected < 0 {
		c.selected = 0
	}
	if c.selected >= len(rows) {
		c.selected = len(rows) - 1
	}
}

// selectedRow returns the selected visible row and whether any row exists.
// An invalid selection returns the last row without changing selected.
func (c *commandsTab) selectedRow() (visRow, bool) {
	rows := c.visibleRows()
	if len(rows) == 0 {
		return visRow{}, false
	}
	if c.selected < 0 || c.selected >= len(rows) {
		return rows[len(rows)-1], true
	}
	return rows[c.selected], true
}

// selectedCommand returns the selected command row when a command row (not a
// project header) is selected.
func (c *commandsTab) selectedCommand() (core.CommandRow, bool) {
	r, ok := c.selectedRow()
	if !ok || r.kind != visCommand {
		return core.CommandRow{}, false
	}
	return c.groups[r.group].Commands[r.cmd], true
}

// moveSelection applies delta across visible rows and clamps the selection.
func (c *commandsTab) moveSelection(delta int) {
	rows := c.visibleRows()
	if len(rows) == 0 {
		return
	}
	c.selected += delta
	if c.selected < 0 {
		c.selected = 0
	}
	if c.selected >= len(rows) {
		c.selected = len(rows) - 1
	}
}

// selectedComposeRow returns the selected compose row and whether any row exists.
// An invalid selection returns the last row without changing selected.
func (t *composeTab) selectedComposeRow() (composeRow, bool) {
	rows := t.visibleRows()
	if len(rows) == 0 {
		return composeRow{}, false
	}
	if t.selected < 0 || t.selected >= len(rows) {
		return rows[len(rows)-1], true
	}
	return rows[t.selected], true
}

func (t *composeTab) visibleRows() []composeRow {
	if t.filter == "" {
		return t.rows
	}
	var out []composeRow
	for _, r := range t.rows {
		if composeRowMatches(t.filter, r) {
			out = append(out, r)
		}
	}
	return out
}

func (t *composeTab) moveSelection(delta int) {
	rows := t.visibleRows()
	if len(rows) == 0 {
		return
	}
	t.selected += delta
	if t.selected < 0 {
		t.selected = 0
	}
	if t.selected >= len(rows) {
		t.selected = len(rows) - 1
	}
}

// setGroups replaces the command groups, sorting active (cwd-tied) projects
// first, then by name, and marks groups active by comparing workdir to cwd.
func (m *Model) setGroups(groups []core.ProjectGroup) {
	for i := range groups {
		groups[i].Active = groups[i].Workdir != "" && groups[i].Workdir == m.cwd
	}
	slices.SortStableFunc(groups, func(a, b core.ProjectGroup) int {
		if a.Active != b.Active {
			return core.BoolFirst(a.Active) // active first
		}
		return cmp.Compare(a.Name, b.Name)
	})
	m.commands.groups = groups
	m.commands.clampSelection()
}

func (m *Model) setComposeRows(rows []composeRow) {
	for i := range rows {
		rows[i].active = rows[i].workdir != "" && rows[i].workdir == m.cwd
	}
	slices.SortStableFunc(rows, func(a, b composeRow) int {
		if a.active != b.active {
			return core.BoolFirst(a.active)
		}
		return cmp.Compare(a.name, b.name)
	})
	m.compose.rows = rows
	if m.compose.selected >= len(m.compose.visibleRows()) {
		m.compose.selected = 0
	}
}

// reportedText words what a command reported about itself: the status with its
// detail in parentheses (D12). It is empty when the command reported nothing,
// which includes every command with no live monitor.
func reportedText(c core.CommandRow) string {
	if c.Status == "" || c.Detail == "" {
		return c.Status
	}
	return c.Status + " (" + c.Detail + ")"
}

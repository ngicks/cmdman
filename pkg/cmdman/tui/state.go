package tui

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/charmbracelet/x/vt"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
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
	backend Backend
	version string

	// ctx is the program-scoped context used to spawn background readers and
	// service calls from bubbletea commands. It is set by Run; tests that drive
	// Update directly may leave it nil.
	ctx context.Context

	width, height int

	active Tab

	commands commandsTab
	compose  composeTab
	layout   layoutTab

	popup     popupState
	helpOpen  bool
	defViewer defViewerState
	composeUp composeUpState

	status string // transient status/error message in the footer
	cwd    string // normalized working directory for active detection

	events    EventStream // lifecycle change-signal subscription
	reloadGen int         // debounce generation for event-triggered re-list

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

// commandRow is a single compose-managed command in the Commands tab.
type commandRow struct {
	id        string
	name      string
	project   string
	workdir   string
	state     model.EventType
	exitCode  *int
	logDriver logdriver.LogDriver
	tty       bool   // command runs under a pseudo-terminal (preview predicate)
	pending   string // pending action label; empty when no action is in flight
}

// projectGroup groups command rows under a compose project.
type projectGroup struct {
	name     string
	workdir  string
	active   bool
	commands []commandRow
}

// key uniquely identifies a project group across refreshes (workdir+name).
func (g projectGroup) key() string { return g.workdir + "\x00" + g.name }

type commandsTab struct {
	groups    []projectGroup
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

// layoutRow is a single mux layout in the Layout tab.
type layoutRow struct {
	name string
}

// moveSelection moves the layout selection by delta, clamped to the rows.
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
// vt emulator (term) sized to the preview pane.
type previewState struct {
	cmdID  string
	lines  []string
	status previewStatus
	errMsg string
	stream LogStream // live Tail+Follow reader for cmdID; nil when none

	terminal     bool             // terminal-view mode (vt emulator) is active
	streaming    bool             // raw drain is live; the repaint tick runs while true
	gen          int              // generation of the active drain/tick loop (see Model.previewGen)
	raw          RawStream        // live raw attach stream for cmdID; nil when none
	term         *vt.SafeEmulator // vt emulator for terminal-view; nil when none
	termW, termH int              // emulator size, tracked so resizes are idempotent
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
	stream  ComposeUpStream          // event source; nil when none
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

// folded reports whether the group at index gi is folded.
func (c *commandsTab) folded(gi int) bool {
	if gi < 0 || gi >= len(c.groups) {
		return false
	}
	return c.fold[c.groups[gi].key()]
}

// setFolded records the fold state for a group.
func (c *commandsTab) setFolded(gi int, v bool) {
	if gi < 0 || gi >= len(c.groups) {
		return
	}
	c.fold[c.groups[gi].key()] = v
}

// visibleRows computes the flattened visible rows honoring the filter and fold
// state. While a filter is active, fold is ignored so matches are reachable.
func (c *commandsTab) visibleRows() []visRow {
	var rows []visRow
	filtering := c.filter != ""
	for gi := range c.groups {
		g := &c.groups[gi]
		projMatch := filtering && matchesFilter(c.filter, g.name)
		var matched []int
		for ci := range g.commands {
			if !filtering || projMatch || commandMatches(c.filter, g.commands[ci]) {
				matched = append(matched, ci)
			}
		}
		if filtering && !projMatch && len(matched) == 0 {
			continue
		}
		// Standalone commands carry no compose project name; list them directly
		// without a (foldable) group header.
		if g.name != "" {
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

// clampSelection keeps selected within [0, len(rows)).
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

// selectedRow returns the currently selected visible row and whether one exists.
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
func (c *commandsTab) selectedCommand() (commandRow, bool) {
	r, ok := c.selectedRow()
	if !ok || r.kind != visCommand {
		return commandRow{}, false
	}
	return c.groups[r.group].commands[r.cmd], true
}

// moveSelection moves the selection by delta across visible rows only.
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

// selectedComposeRow returns the highlighted compose row.
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

// visibleRows returns the compose rows that match the active filter.
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
func (m *Model) setGroups(groups []projectGroup) {
	for i := range groups {
		groups[i].active = groups[i].workdir != "" && groups[i].workdir == m.cwd
	}
	slices.SortStableFunc(groups, func(a, b projectGroup) int {
		if a.active != b.active {
			return boolFirst(a.active) // active first
		}
		return cmp.Compare(a.name, b.name)
	})
	m.commands.groups = groups
	m.commands.clampSelection()
}

// setComposeRows replaces the Compose-tab rows, sorting active projects first.
func (m *Model) setComposeRows(rows []composeRow) {
	for i := range rows {
		rows[i].active = rows[i].workdir != "" && rows[i].workdir == m.cwd
	}
	slices.SortStableFunc(rows, func(a, b composeRow) int {
		if a.active != b.active {
			return boolFirst(a.active)
		}
		return cmp.Compare(a.name, b.name)
	})
	m.compose.rows = rows
	if m.compose.selected >= len(m.compose.visibleRows()) {
		m.compose.selected = 0
	}
}

// boolFirst returns -1 when v is true so that active entries sort before
// inactive ones in a stable three-way comparator. Only call it when the two
// compared booleans differ.
func boolFirst(v bool) int {
	if v {
		return -1
	}
	return 1
}

// displayLabel maps a persisted state to a friendly display label. The label is
// the state value itself, except an exited command annotates its exit code. All
// logic elsewhere uses the real state values; this is presentation only.
func displayLabel(state model.EventType, exitCode *int) string {
	if state == model.EventTypeExited && exitCode != nil {
		return fmt.Sprintf("exited(%d)", *exitCode)
	}
	return string(state)
}

// groupFromInfos builds project groups from flat command infos, grouping by
// (workdir, project).
func groupFromInfos(infos []CommandInfo) []projectGroup {
	idx := map[string]int{}
	var groups []projectGroup
	for _, ci := range infos {
		k := ci.Workdir + "\x00" + ci.Project
		gi, ok := idx[k]
		if !ok {
			gi = len(groups)
			idx[k] = gi
			groups = append(groups, projectGroup{name: ci.Project, workdir: ci.Workdir})
		}
		groups[gi].commands = append(groups[gi].commands, commandRow{
			id:        ci.ID,
			name:      ci.Name,
			project:   ci.Project,
			workdir:   ci.Workdir,
			state:     ci.State,
			exitCode:  ci.ExitCode,
			logDriver: ci.LogDriver,
			tty:       ci.Tty,
		})
	}
	return groups
}

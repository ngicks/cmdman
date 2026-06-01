package tui

import (
	"context"
	"fmt"
	"sort"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// tab identifies a top-level tab.
type tab int

const (
	tabCommands tab = iota
	tabCompose
)

const numTabs = 2

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

	active tab

	commands commandsTab
	compose  composeTab

	popup    popupState
	helpOpen bool

	status string // transient status/error message in the footer
	cwd    string // normalized working directory for active detection

	events    EventStream // lifecycle change-signal subscription
	reloadGen int         // debounce generation for event-triggered re-list

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

// previewState holds the right-pane preview content for the selected command.
type previewState struct {
	cmdID  string
	lines  []string
	status previewStatus
	errMsg string
	scroll int
	stream LogStream // live Tail+Follow reader for cmdID; nil when none
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
		rows = append(rows, visRow{kind: visProject, group: gi})
		// When filtering, force-expand so matches are visible; otherwise honor fold.
		if !filtering && c.folded(gi) {
			continue
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
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].active != groups[j].active {
			return groups[i].active // active first
		}
		return groups[i].name < groups[j].name
	})
	m.commands.groups = groups
	m.commands.clampSelection()
}

// setComposeRows replaces the Compose-tab rows, sorting active projects first.
func (m *Model) setComposeRows(rows []composeRow) {
	for i := range rows {
		rows[i].active = rows[i].workdir != "" && rows[i].workdir == m.cwd
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].active != rows[j].active {
			return rows[i].active
		}
		return rows[i].name < rows[j].name
	})
	m.compose.rows = rows
	if m.compose.selected >= len(m.compose.visibleRows()) {
		m.compose.selected = 0
	}
}

// displayLabel maps a persisted state to a friendly display label. All logic
// elsewhere uses the real state values; this is presentation only.
func displayLabel(state model.EventType, exitCode *int) string {
	switch state {
	case model.EventTypeStarted:
		return "running"
	case model.EventTypeStarting:
		return "starting"
	case model.EventTypeCreated:
		return "created"
	case model.EventTypeExited:
		if exitCode != nil {
			return fmt.Sprintf("exited(%d)", *exitCode)
		}
		return "exited"
	case model.EventTypeFailed:
		return "failed"
	default:
		return string(state)
	}
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
		})
	}
	return groups
}

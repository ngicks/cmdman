package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/mattn/go-runewidth"
	"github.com/ngicks/cmdman/cmdman/model"
)

// TestWidgetDefsInSync guards the invariant that WidgetKeys and ParseWidget both
// derive from widgetDefs, so the `tui widget <name>` subcommands and the frame
// component names can never drift. WidgetNone names no widget and must not
// parse.
func TestWidgetDefsInSync(t *testing.T) {
	keys := WidgetKeys()
	if len(keys) != len(widgetDefs) {
		t.Fatalf("WidgetKeys() len = %d, want %d", len(keys), len(widgetDefs))
	}
	for i, d := range widgetDefs {
		if keys[i] != d.key {
			t.Errorf("WidgetKeys()[%d] = %q, want %q", i, keys[i], d.key)
		}
		got, err := ParseWidget(d.key)
		if err != nil {
			t.Errorf("ParseWidget(%q) unexpected error: %v", d.key, err)
		}
		if got != d.widget {
			t.Errorf("ParseWidget(%q) = %d, want %d", d.key, got, d.widget)
		}
		if d.widget == WidgetNone {
			t.Errorf("widgetDefs[%d] maps %q to WidgetNone", i, d.key)
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
// widget runs the restricted single-view model, and leaving it unset (the zero
// value every existing caller passes) still runs the full multi-tab model.
func TestWidgetOptionSelectsModel(t *testing.T) {
	ctx := context.Background()
	fb := &fakeBackend{}

	full := newProgramModel(ctx, Options{Backend: fb, InitialTab: TabCompose})
	m, ok := full.(Model)
	if !ok {
		t.Fatalf("Widget unset should run the full model, got %T", full)
	}
	if m.active != TabCompose {
		t.Errorf("full model active tab = %d, want %d", m.active, TabCompose)
	}

	for _, w := range []Widget{WidgetSwitcher, WidgetStatusbar} {
		got := newProgramModel(ctx, Options{Backend: fb, Widget: w})
		wm, ok := got.(widgetModel)
		if !ok {
			t.Fatalf("Widget %d should run the widget model, got %T", w, got)
		}
		if wm.widget != w {
			t.Errorf("widget model widget = %d, want %d", wm.widget, w)
		}
		if wm.ctx == nil {
			t.Errorf("widget model should carry the program context")
		}
	}
}

func TestSwitcherGroupsJoin(t *testing.T) {
	cmds := []CommandInfo{
		{ID: "1", Name: "web", Project: "api", Workdir: "/work/api",
			State: model.EventTypeRunning},
		{ID: "2", Name: "db", Project: "api", Workdir: "/work/api",
			State: model.EventTypeExited},
		// A never-run named def carries no workdir in the project listing, so
		// its commands join by name alone.
		{ID: "3", Name: "site", Project: "blog", Workdir: "/work/blog",
			State: model.EventTypeRunning},
		// Same project name, different directory: a distinct project.
		{ID: "4", Name: "web", Project: "api", Workdir: "/other/api",
			State: model.EventTypeRunning},
		// Standalone: no compose project, so no project window to switch to.
		{ID: "5", Name: "loose", Workdir: "/work/loose",
			State: model.EventTypeRunning},
	}
	projs := []ProjectInfo{
		{Name: "api", Workdir: "/work/api"},
		{Name: "blog"},
		{Name: "never-run", Workdir: "/work/never"},
	}

	// No title stamps: every command falls into the no-title bucket, so the
	// ordering under test is the one inside a bucket (by name).
	groups := switcherGroups(projs, cmds, "/work/blog", nil)

	var got []string
	for _, g := range groups {
		names := make([]string, 0, len(g.commands))
		for _, c := range g.commands {
			names = append(names, c.name)
		}
		got = append(got, g.name+"@"+g.workdir+"["+strings.Join(names, ",")+"]")
	}
	want := []string{
		// cwd-active project first, then by name; the orphaned /other/api
		// group is kept as its own project.
		"blog@/work/blog[site]",
		"api@/work/api[db,web]",
		"api@/other/api[web]",
		"never-run@/work/never[]",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("switcherGroups()\n got %v\nwant %v", got, want)
	}
	if !groups[0].active {
		t.Errorf("the cwd project should be marked active")
	}
}

// TestSwitcherRenderAlignment renders the grouped list and checks the property
// the marker/width math exists for: every group line is exactly the pane width,
// so the selected group is a solid block and nothing overflows the column.
func TestSwitcherRenderAlignment(t *testing.T) {
	const w, h = 32, 12
	m := seedWidget(WidgetSwitcher, w, h)

	lines := m.switcherLines(w)
	if len(lines) != 5 { // 2 projects + 3 commands
		t.Fatalf("switcherLines() = %d lines, want 5", len(lines))
	}
	for i, l := range lines {
		if got := lipgloss.Width(l.text); got != w {
			t.Errorf("line %d width = %d, want %d (%q)", i, got, w, l.text)
		}
	}

	out := m.viewContent()
	for _, want := range []string{"projects", "local-dev", "api-stack", "watcher", "seed-db"} {
		if !strings.Contains(out, want) {
			t.Errorf("switcher view should contain %q:\n%s", want, out)
		}
	}
	if got := len(strings.Split(out, "\n")); got > h {
		t.Errorf("switcher view = %d lines, must fit the %d-row pane", got, h)
	}
}

// TestSwitcherFitsShortPanes pins the property a frame def can violate at will:
// a switcher docked into a two-cell strip must still fit its pane, chrome and
// all, whether or not there are projects to list.
func TestSwitcherFitsShortPanes(t *testing.T) {
	const w = 30
	full := seedWidget(WidgetSwitcher, w, 12)
	empty := seedWidget(WidgetSwitcher, w, 12)
	empty.groups = nil

	for _, m := range []widgetModel{full, empty} {
		for h := 1; h <= 6; h++ {
			m.height = h
			out := m.viewContent()
			if got := len(strings.Split(out, "\n")); got > h {
				t.Errorf("switcher in a %d-row pane rendered %d lines:\n%s", h, got, out)
			}
		}
	}
}

// TestSwitcherMarker covers the one marker slot a project head carries: the
// aggregation that picks the dot's color (D14 as amended by D22 — the bell no
// longer competes in it) and the bell that replaces the dot entirely (D23).
func TestSwitcherMarker(t *testing.T) {
	cmd := func(name, status string, bell bool) commandRow {
		return commandRow{
			id: name, name: name, state: model.EventTypeRunning,
			status: status, bell: bell,
		}
	}
	dead := func(name, status string, bell bool) commandRow {
		c := cmd(name, status, bell)
		c.state = model.EventTypeExited
		return c
	}
	for _, tc := range []struct {
		name  string
		cmds  []commandRow
		agg   string
		glyph string
		fg    lipgloss.Style
	}{
		{
			name:  "a project whose commands said nothing is hollow green",
			cmds:  []commandRow{cmd("a", "", false), cmd("b", "", false)},
			glyph: glyphHollow,
			fg:    styleMarkerIdle,
		},
		{
			name:  "a project with no commands at all reads the same",
			glyph: glyphHollow,
			fg:    styleMarkerIdle,
		},
		{
			name:  "done fills the circle without changing its color",
			cmds:  []commandRow{cmd("a", statusDone, false), cmd("b", "", false)},
			agg:   statusDone,
			glyph: glyphFilled,
			fg:    styleMarkerIdle,
		},
		{
			name:  "working outranks done",
			cmds:  []commandRow{cmd("a", statusDone, false), cmd("b", statusWorking, false)},
			agg:   statusWorking,
			glyph: glyphFilled,
			fg:    styleMarkerWork,
		},
		{
			name: "waiting outranks working",
			cmds: []commandRow{
				cmd("a", statusWorking, false),
				cmd("b", statusWaiting, false),
				cmd("c", statusDone, false),
			},
			agg:   statusWaiting,
			glyph: glyphFilled,
			fg:    styleMarkerBlocked,
		},
		{
			name: "an unread bell anywhere takes the slot from the dot",
			cmds: []commandRow{
				cmd("a", statusWorking, false),
				cmd("b", statusDone, true),
			},
			agg:   statusWorking,
			glyph: glyphBell,
			fg:    styleMarkerWork,
		},
		{
			name: "a finished run's report and bell no longer speak for it",
			cmds: []commandRow{
				dead("a", statusWaiting, true),
				cmd("b", "", false),
			},
			glyph: glyphHollow,
			fg:    styleMarkerIdle,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := projectGroup{name: "proj", commands: tc.cmds}
			if got := aggregateStatus(g); got != tc.agg {
				t.Errorf("aggregateStatus() = %q, want %q", got, tc.agg)
			}
			if got := markerGlyph(g); got != tc.glyph {
				t.Errorf("markerGlyph() = %q, want %q", got, tc.glyph)
			}
			if got, want := markerStyle(g).GetForeground(), tc.fg.GetForeground(); got != want {
				t.Errorf("markerStyle() foreground = %v, want %v", got, want)
			}
		})
	}
}

// TestBucketSortOrdersByTitleBucket covers D20's ordering: commands whose
// titles were updated in the same ~5 s bucket keep a stable name order instead
// of trading places, a newer bucket floats above an older one, and a command
// with no title at all sinks below the ones that said something.
func TestBucketSortOrdersByTitleBucket(t *testing.T) {
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cmds := []commandRow{
		{id: "1", name: "zebra"},   // no title: sinks
		{id: "2", name: "beta"},    // oldest bucket
		{id: "3", name: "delta"},   // newest bucket, later within it
		{id: "4", name: "charlie"}, // newest bucket, earlier within it
		{id: "5", name: "alpha"},   // middle bucket
	}
	titles := map[string]titleStamp{
		"2": {title: "b", at: base},
		"3": {title: "d", at: base.Add(4 * titleBucket).Add(4 * time.Second)},
		"4": {title: "c", at: base.Add(4 * titleBucket)},
		"5": {title: "a", at: base.Add(2 * titleBucket)},
	}

	bucketSort(cmds, titles)

	var got []string
	for _, c := range cmds {
		got = append(got, c.name)
	}
	want := []string{"charlie", "delta", "alpha", "beta", "zebra"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("bucketSort() = %v, want %v", got, want)
	}
}

// TestStampTitlesDatesChangesOnly covers where the bucket times come from: the
// monitor serves no title timestamp, so a title that did not change must keep
// the time it was first seen with — otherwise every refresh would restamp
// everything into one bucket and the sort would say nothing.
func TestStampTitlesDatesChangesOnly(t *testing.T) {
	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	now := t0
	m := widgetModel{now: func() time.Time { return now }}

	m.cmds = []CommandInfo{
		{ID: "1", Title: "first"},
		{ID: "2", Title: ""},
		{ID: "3", Title: "gone soon"},
	}
	m.titles = m.stampTitles()
	if got := m.titles["1"]; got.at != t0 || got.title != "first" {
		t.Fatalf("first load stamp = %+v, want first@%v", got, t0)
	}
	if _, ok := m.titles["2"]; ok {
		t.Errorf("a command with no title should carry no stamp")
	}

	now = t0.Add(30 * time.Second)
	m.cmds = []CommandInfo{
		{ID: "1", Title: "first"},
		{ID: "2", Title: "now titled"},
	}
	m.titles = m.stampTitles()
	if got := m.titles["1"].at; got != t0 {
		t.Errorf("an unchanged title should keep its stamp, got %v want %v", got, t0)
	}
	if got := m.titles["2"].at; got != now {
		t.Errorf("a new title should be stamped now, got %v want %v", got, now)
	}
	if _, ok := m.titles["3"]; ok {
		t.Errorf("a command that disappeared should drop out of the stamps")
	}

	now = t0.Add(60 * time.Second)
	m.cmds = []CommandInfo{{ID: "1", Title: "retitled"}}
	m.titles = m.stampTitles()
	if got := m.titles["1"].at; got != now {
		t.Errorf("a retitle should start a new bucket, got %v want %v", got, now)
	}
}

// TestSwitcherRowsShowRuntimeState pins what a command row says once the
// monitor answers: the reported status for a live run, the title it set, the
// unread bell — and, for a run that is over, its exit state rather than any
// report (D13).
func TestSwitcherRowsShowRuntimeState(t *testing.T) {
	m := seedWidget(WidgetSwitcher, 60, 12)
	next, _ := m.Update(commandsLoadedMsg{infos: []CommandInfo{
		{
			ID: "1", Name: "agent", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeRunning, Title: "review mon_run.go",
			Status: statusWaiting, Detail: "needs input", BellUnread: true,
		},
		{
			ID: "2", Name: "seed-db", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeExited, ExitCode: new(0), Status: statusWorking,
		},
	}})
	m = next.(widgetModel)

	out := m.viewContent()
	for _, want := range []string{"agent", statusWaiting, "review mon_run.go", glyphBell} {
		if !strings.Contains(out, want) {
			t.Errorf("switcher should show %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "exited") {
		t.Errorf("an exited command should show its exit state:\n%s", out)
	}
	if strings.Contains(out, statusWorking) {
		t.Errorf("an exited command must not show its last report (D13):\n%s", out)
	}
}

// TestMarkerWidthsMatchRenderer pins the ruler the marker slot is measured
// with. ● and ○ are East-Asian *Ambiguous*, so under a CJK locale
// go-runewidth's package default calls them two cells while the renderer draws
// one; padding a slot with one ruler and drawing it with the other tears the
// column apart. The locale is forced here so the assertion holds everywhere.
func TestMarkerWidthsMatchRenderer(t *testing.T) {
	prev := runewidth.DefaultCondition.EastAsianWidth
	runewidth.DefaultCondition.EastAsianWidth = true
	t.Cleanup(func() { runewidth.DefaultCondition.EastAsianWidth = prev })

	for _, g := range []string{glyphBell, glyphFilled, glyphHollow} {
		if got, want := glyphWidth(g), lipgloss.Width(g); got != want {
			t.Errorf("glyphWidth(%q) = %d, renderer draws %d", g, got, want)
		}
	}
	if runewidth.StringWidth(glyphHollow) == lipgloss.Width(glyphHollow) {
		t.Errorf("the package default now agrees with the renderer; " +
			"the explicit Condition may no longer be load-bearing")
	}
}

// TestSwitcherViewportFollowsSelection checks that a selection below the fold
// scrolls the list instead of being truncated at the pane edge.
func TestSwitcherViewportFollowsSelection(t *testing.T) {
	lines := []switcherLine{
		{group: 0}, {group: 0},
		{group: 1}, {group: 1},
		{group: 2}, {group: 2},
	}
	if got := viewportOffset(lines, 0, 3); got != 0 {
		t.Errorf("offset for the first group = %d, want 0", got)
	}
	// The last group's rows are lines 4-5; a 3-row viewport must scroll to
	// include them while keeping the group's head line visible.
	if got := viewportOffset(lines, 2, 3); got != 3 {
		t.Errorf("offset for the last group = %d, want 3", got)
	}
	if got := viewportOffset(lines[:2], 0, 3); got != 0 {
		t.Errorf("offset with everything visible = %d, want 0", got)
	}
}

func TestSwitcherKeysMoveSelection(t *testing.T) {
	m := seedWidget(WidgetSwitcher, 32, 12)
	next, _ := m.Update(kr("j"))
	m = next.(widgetModel)
	if m.selected != 1 {
		t.Fatalf("j should move the selection down, got %d", m.selected)
	}
	next, _ = m.Update(kr("k"))
	m = next.(widgetModel)
	if m.selected != 0 {
		t.Fatalf("k should move the selection up, got %d", m.selected)
	}
	// The selection never leaves the list.
	next, _ = m.Update(kr("k"))
	if next.(widgetModel).selected != 0 {
		t.Fatalf("selection should clamp at the first group")
	}

	next, cmd := m.Update(kr("q"))
	if !next.(widgetModel).quitting || cmd == nil {
		t.Fatalf("q should quit the widget")
	}
}

// TestStatusbarRendersOneLine pins the shape a one-row pane needs: exactly one
// line, exactly the pane width.
func TestStatusbarRendersOneLine(t *testing.T) {
	const w = 60
	m := seedWidget(WidgetStatusbar, w, 1)

	out := m.viewContent()
	if strings.Contains(out, "\n") {
		t.Fatalf("statusbar must render a single line, got:\n%q", out)
	}
	if got := lipgloss.Width(out); got != w {
		t.Errorf("statusbar width = %d, want %d", got, w)
	}
	for _, want := range []string{"local-dev", "2 projects", "2 running"} {
		if !strings.Contains(out, want) {
			t.Errorf("statusbar should contain %q: %q", want, out)
		}
	}

	// A pane too narrow for the version keeps where-you-are and still fills
	// its row exactly.
	narrow := seedWidget(WidgetStatusbar, 14, 1)
	line := narrow.viewContent()
	if got := lipgloss.Width(line); got != 14 {
		t.Errorf("narrow statusbar width = %d, want 14", got)
	}

	// The marker is the widest glyph there is when a bell is unread, and the
	// bar's padding is computed against it: a two-cell marker must not push the
	// row past its pane.
	next, _ := m.Update(commandsLoadedMsg{infos: []CommandInfo{{
		ID: "1", Name: "agent", Project: "local-dev", Workdir: "/work/local-dev",
		State: model.EventTypeRunning, BellUnread: true,
	}}})
	belled := next.(widgetModel).viewContent()
	if !strings.Contains(belled, glyphBell) {
		t.Errorf("an unread bell should reach the statusbar marker: %q", belled)
	}
	if got := lipgloss.Width(belled); got != w {
		t.Errorf("statusbar width with a bell = %d, want %d", got, w)
	}
}

// seedWidget builds a widget model over two projects — local-dev is the
// cwd-tied one — driven through the same load messages the program delivers.
func seedWidget(w Widget, width, height int) widgetModel {
	fb := &fakeBackend{
		cwd: "/work/local-dev",
		cmds: []CommandInfo{
			{ID: "1", Name: "watcher", Project: "local-dev", Workdir: "/work/local-dev",
				State: model.EventTypeRunning},
			{ID: "2", Name: "seed-db", Project: "local-dev", Workdir: "/work/local-dev",
				State: model.EventTypeExited},
			{ID: "3", Name: "web", Project: "api-stack", Workdir: "/work/api",
				State: model.EventTypeRunning},
		},
		projs: []ProjectInfo{
			{Name: "local-dev", Workdir: "/work/local-dev"},
			{Name: "api-stack", Workdir: "/work/api"},
		},
	}
	m := newWidget(Options{Backend: fb, Widget: w, Version: "v0.0.0-test"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(widgetModel)
	next, _ = m.Update(commandsLoadedMsg{infos: fb.cmds})
	m = next.(widgetModel)
	next, _ = m.Update(projectsLoadedMsg{infos: fb.projs})
	return next.(widgetModel)
}

package panel

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

func TestSwitcherGroupsJoin(t *testing.T) {
	cmds := []core.CommandInfo{
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
	projs := []core.ProjectInfo{
		{Name: "api", Workdir: "/work/api"},
		{Name: "blog"},
		{Name: "never-run", Workdir: "/work/never"},
	}

	// No title stamps: every command falls into the no-title bucket, so the
	// ordering under test is the one inside a bucket (by name).
	groups := switcherGroups(projs, cmds, "/work/blog", nil)

	var got []string
	for _, g := range groups {
		names := make([]string, 0, len(g.Commands))
		for _, c := range g.Commands {
			names = append(names, c.Name)
		}
		got = append(got, g.Name+"@"+g.Workdir+"["+strings.Join(names, ",")+"]")
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
	if !groups[0].Active {
		t.Errorf("the cwd project should be marked active")
	}
}

// TestSwitcherRenderAlignment renders the grouped list and checks the property
// the marker/width math exists for: every group line is exactly the pane width,
// so the selected group is a solid block and nothing overflows the column.
func TestSwitcherRenderAlignment(t *testing.T) {
	const w, h = 32, 12
	m := seedWidget(core.WidgetSwitcher, w, h)

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
	// The heads name their directories (D44), so the project names are gone from
	// the view and the paths are what has to be there.
	for _, want := range []string{
		"projects", "/work/local-dev", "/work/api", "watcher", "seed-db",
	} {
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
	full := seedWidget(core.WidgetSwitcher, w, 12)
	empty := seedWidget(core.WidgetSwitcher, w, 12)
	empty.groups = nil

	for _, m := range []Model{full, empty} {
		for h := 1; h <= 6; h++ {
			m.height = h
			out := m.viewContent()
			lines := strings.Split(out, "\n")
			if got := len(lines); got > h {
				t.Errorf("switcher in a %d-row pane rendered %d lines:\n%s", h, got, out)
			}
			// Width counts too: a chrome line wider than the column wraps, which
			// costs the list a row nobody gave it.
			for i, line := range lines {
				if got := lipgloss.Width(line); got > w {
					t.Errorf("line %d in a %d-row pane is %d cells wide: %q", i, h, got, line)
				}
			}
		}
	}
}

// TestSwitcherHeadsNameTheirDirectory covers D44's head line: a group heads
// with the directory it sits in rather than with its project name — several
// compose projects can run on one directory, so the name misidentifies the
// place — and only the groups actually sharing one add their name back.
func TestSwitcherHeadsNameTheirDirectory(t *testing.T) {
	m := seedWidget(core.WidgetSwitcher, 60, 12)

	out := m.viewContent()
	for _, want := range []string{"/work/local-dev", "/work/api"} {
		if !strings.Contains(out, want) {
			t.Errorf("a head should name its directory %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "api-stack") {
		t.Errorf("a head that names its directory should not name its project:\n%s", out)
	}

	// Two projects in one directory: there the path no longer says which is
	// which, so those heads carry their project names — and the third does not.
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: nil})
	m, _ = updWidget(t, m, core.ProjectsLoadedMsg{Infos: []core.ProjectInfo{
		{Name: "alpha", Workdir: "/work/shared", Identity: "id-alpha"},
		{Name: "beta", Workdir: "/work/shared", Identity: "id-beta"},
		{Name: "solo", Workdir: "/work/solo", Identity: "id-solo"},
	}})

	out = m.viewContent()
	for _, want := range []string{"/work/shared (alpha)", "/work/shared (beta)"} {
		if !strings.Contains(out, want) {
			t.Errorf("a shared directory should be told apart by %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/work/solo (") {
		t.Errorf("a directory only one project sits in needs no project name:\n%s", out)
	}
}

// TestSwitcherActiveHeadKeepsItsMarker pins what the head's new budget is for:
// a path fills a docked column where a project name never did, so the row has
// to shorten the path rather than lose the one cue saying you are standing in
// that project. 40 cells is the width the frame fixtures dock a switcher at.
func TestSwitcherActiveHeadKeepsItsMarker(t *testing.T) {
	const w = 40
	m := seedWidget(core.WidgetSwitcher, w, 12)
	g := core.ProjectGroup{
		Name:    "cmdman",
		Workdir: "/work/aaaa/bbbb/cccc/dddd/eeee",
		Active:  true,
	}

	line := core.StripANSI(core.PadLine(m.headLine(g, core.BgNone, false, w), w, core.BgNone))
	if !strings.Contains(line, "active") {
		t.Errorf("an active head should keep its marker at %d cells: %q", w, line)
	}
	if !strings.Contains(line, "/eeee") {
		t.Errorf("a shortened path should still end where it does: %q", line)
	}
	if got := core.Cells.StringWidth(line); got != w {
		t.Errorf("head line = %d cells, want %d: %q", got, w, line)
	}
}

// TestSwitcherHeadLabel covers what a head says on its own, including the two
// groups a directory cannot speak for: one that has never run anywhere in
// particular, and one whose column is narrower than its path.
func TestSwitcherHeadLabel(t *testing.T) {
	for _, tc := range []struct {
		name string
		g    core.ProjectGroup
		dup  bool
		w    int
		want string
	}{
		{
			name: "the directory heads the group",
			g:    core.ProjectGroup{Name: "api", Workdir: "/work/api"},
			w:    40,
			want: "/work/api",
		},
		{
			name: "a shared directory adds the project name",
			g:    core.ProjectGroup{Name: "api", Workdir: "/work/api"},
			dup:  true,
			w:    40,
			want: "/work/api (api)",
		},
		{
			name: "a shared directory says so even for a nameless group",
			g:    core.ProjectGroup{Workdir: "/work/api"},
			dup:  true,
			w:    40,
			want: "/work/api (unnamed)",
		},
		{
			name: "a never-run def has only its name to give",
			g:    core.ProjectGroup{Name: "blog"},
			w:    40,
			want: "blog",
		},
		{
			name: "a group with neither keeps the old fallback",
			w:    40,
			want: "(unnamed)",
		},
		{
			name: "a path too long for the column keeps its tail",
			g:    core.ProjectGroup{Name: "api", Workdir: "/work/api/deep"},
			w:    12,
			want: "…rk/api/deep",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := headLabel(tc.g, tc.dup, tc.w); got != tc.want {
				t.Errorf("headLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSwitcherHeadAbbreviatesHome pins the abbreviation the heads share with
// the launcher's paths: a directory under the user's own home is written "~/…"
// rather than spending the column on a prefix every row would repeat.
func TestSwitcherHeadAbbreviatesHome(t *testing.T) {
	home := core.HomeDir()
	if home == "" {
		t.Skip("no home directory for this process to abbreviate against")
	}
	g := core.ProjectGroup{Name: "api", Workdir: home + "/src/app"}
	if got, want := headLabel(g, false, 40), "~/src/app"; got != want {
		t.Errorf("headLabel() = %q, want %q", got, want)
	}
}

// TestSwitcherWidthsSurviveWidePathsAndBadges pins the cell math the head's
// path and the row's badge go through. A double-width path cut by rune count
// comes back twice as wide as the column that asked for it, which would push
// every line past its pane; the badge has to fit the same ruler.
func TestSwitcherWidthsSurviveWidePathsAndBadges(t *testing.T) {
	const dir = "/work/作業場/日本語のディレクトリ"
	m := seedWidget(core.WidgetSwitcher, 24, 12)
	m, _ = updWidget(t, m, core.ProjectsLoadedMsg{Infos: []core.ProjectInfo{
		{Name: "アプリ", Workdir: dir, Identity: "id-app"},
	}})
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: []core.CommandInfo{{
		ID: "1", Name: "ワーカー", Project: "アプリ", Workdir: dir,
		State: model.EventTypeRunning, ScaleIndex: 2, ScaleCount: 3,
	}}})

	for w := 8; w <= 40; w++ {
		for i, l := range m.switcherLines(w) {
			if got := lipgloss.Width(l.text); got != w {
				t.Fatalf("width %d: line %d = %d cells (%q)", w, i, got, l.text)
			}
		}
	}
}

// TestScaleBadgeMarksReplicasOnly covers the badge's guard at both its edges: a
// command that is one replica among several says which one, and neither half of
// the unscaled zero value invents an answer. The listing already collapses a
// sole instance to that zero pair (cli.commandInfos), so what the guard here
// keeps out is a {1,1} pair reaching a row and badging an unscaled command.
func TestScaleBadgeMarksReplicasOnly(t *testing.T) {
	for _, tc := range []struct {
		name         string
		index, count int
		want         string
	}{
		{"one replica among several", 2, 3, " [2/3]"},
		{"a sole instance is not scaled", 1, 1, ""},
		{"a count with no index says nothing", 0, 3, ""},
		{"an index with no count says nothing", 2, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := core.CommandRow{
				ID: "1", Name: "web", State: model.EventTypeRunning,
				ScaleIndex: tc.index, ScaleCount: tc.count,
			}
			if got := core.ScaleBadge(c); got != tc.want {
				t.Errorf("core.ScaleBadge(%d/%d) = %q, want %q",
					tc.index, tc.count, got, tc.want)
			}
		})
	}
}

// TestSwitcherRowsBadgeReplicas is the badge where it was asked for: a scaled
// command's row says which replica it is, the identity survives the load path
// (core.GroupFromInfos) and a run that is over — D13 takes a command's words
// away, not what it is — and an unscaled row says nothing new.
func TestSwitcherRowsBadgeReplicas(t *testing.T) {
	m := seedWidget(core.WidgetSwitcher, 60, 12)
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: []core.CommandInfo{
		{
			ID: "1", Name: "web", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeRunning, ScaleIndex: 2, ScaleCount: 3, Title: "serving",
		},
		{
			ID: "2", Name: "db", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeExited, ExitCode: new(0), ScaleIndex: 1, ScaleCount: 2,
		},
		{
			ID: "3", Name: "solo", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeRunning,
		},
	}})

	out := core.StripANSI(m.viewContent())
	for _, want := range []string{"[2/3]", "[1/2]"} {
		if !strings.Contains(out, want) {
			t.Errorf("a replica's row should carry %q:\n%s", want, out)
		}
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "solo") && strings.Contains(line, "[") {
			t.Errorf("an unscaled command's row should carry no badge: %q", line)
		}
	}
}

// TestSwitcherMarker covers the one marker slot a project head carries: the
// aggregation that picks the dot's color (D14 as amended by D22 — the bell no
// longer competes in it) and the bell that replaces the dot entirely (D23).
func TestSwitcherMarker(t *testing.T) {
	cmd := func(name, status string, bell bool) core.CommandRow {
		return core.CommandRow{
			ID: name, Name: name, State: model.EventTypeRunning,
			Status: status, Bell: bell,
		}
	}
	dead := func(name, status string, bell bool) core.CommandRow {
		c := cmd(name, status, bell)
		c.State = model.EventTypeExited
		return c
	}
	for _, tc := range []struct {
		name  string
		cmds  []core.CommandRow
		agg   string
		glyph string
		fg    lipgloss.Style
	}{
		{
			name:  "a project whose commands said nothing is hollow green",
			cmds:  []core.CommandRow{cmd("a", "", false), cmd("b", "", false)},
			glyph: core.GlyphHollow,
			fg:    core.StyleMarkerIdle,
		},
		{
			name:  "a project with no commands at all reads the same",
			glyph: core.GlyphHollow,
			fg:    core.StyleMarkerIdle,
		},
		{
			name:  "done fills the circle without changing its color",
			cmds:  []core.CommandRow{cmd("a", core.StatusDone, false), cmd("b", "", false)},
			agg:   core.StatusDone,
			glyph: core.GlyphFilled,
			fg:    core.StyleMarkerIdle,
		},
		{
			name: "working outranks done",
			cmds: []core.CommandRow{
				cmd("a", core.StatusDone, false),
				cmd("b", core.StatusWorking, false),
			},
			agg:   core.StatusWorking,
			glyph: core.GlyphFilled,
			fg:    core.StyleMarkerWork,
		},
		{
			name: "waiting outranks working",
			cmds: []core.CommandRow{
				cmd("a", core.StatusWorking, false),
				cmd("b", core.StatusWaiting, false),
				cmd("c", core.StatusDone, false),
			},
			agg:   core.StatusWaiting,
			glyph: core.GlyphFilled,
			fg:    core.StyleMarkerBlocked,
		},
		{
			name: "an unread bell anywhere takes the slot from the dot",
			cmds: []core.CommandRow{
				cmd("a", core.StatusWorking, false),
				cmd("b", core.StatusDone, true),
			},
			agg:   core.StatusWorking,
			glyph: core.GlyphBell,
			fg:    core.StyleMarkerWork,
		},
		{
			name: "a finished run's report and bell no longer speak for it",
			cmds: []core.CommandRow{
				dead("a", core.StatusWaiting, true),
				cmd("b", "", false),
			},
			glyph: core.GlyphHollow,
			fg:    core.StyleMarkerIdle,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := core.ProjectGroup{Name: "proj", Commands: tc.cmds}
			if got := core.AggregateStatus(g); got != tc.agg {
				t.Errorf("core.AggregateStatus() = %q, want %q", got, tc.agg)
			}
			if got := core.MarkerGlyph(g); got != tc.glyph {
				t.Errorf("core.MarkerGlyph() = %q, want %q", got, tc.glyph)
			}
			if got, want := core.MarkerStyle(g).
				GetForeground(),
				tc.fg.GetForeground(); got != want {
				t.Errorf("core.MarkerStyle() foreground = %v, want %v", got, want)
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
	cmds := []core.CommandRow{
		{ID: "1", Name: "zebra"},   // no title: sinks
		{ID: "2", Name: "beta"},    // oldest bucket
		{ID: "3", Name: "delta"},   // newest bucket, later within it
		{ID: "4", Name: "charlie"}, // newest bucket, earlier within it
		{ID: "5", Name: "alpha"},   // middle bucket
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
		got = append(got, c.Name)
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
	m := Model{now: func() time.Time { return now }}

	m.cmds = []core.CommandInfo{
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
	m.cmds = []core.CommandInfo{
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
	m.cmds = []core.CommandInfo{{ID: "1", Title: "retitled"}}
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
	m := seedWidget(core.WidgetSwitcher, 60, 12)
	next, _ := m.Update(core.CommandsLoadedMsg{Infos: []core.CommandInfo{
		{
			ID: "1", Name: "agent", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeRunning, Title: "review mon_run.go",
			Status: core.StatusWaiting, Detail: "needs input", BellUnread: true,
		},
		{
			ID: "2", Name: "seed-db", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeExited, ExitCode: new(0), Status: core.StatusWorking,
		},
	}})
	m = next.(Model)

	out := m.viewContent()
	for _, want := range []string{"agent", core.StatusWaiting, "review mon_run.go", core.GlyphBell} {
		if !strings.Contains(out, want) {
			t.Errorf("switcher should show %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "exited") {
		t.Errorf("an exited command should show its exit state:\n%s", out)
	}
	if strings.Contains(out, core.StatusWorking) {
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

	for _, g := range []string{core.GlyphBell, core.GlyphFilled, core.GlyphHollow} {
		if got, want := core.GlyphWidth(g), lipgloss.Width(g); got != want {
			t.Errorf("core.GlyphWidth(%q) = %d, renderer draws %d", g, got, want)
		}
	}
	if runewidth.StringWidth(core.GlyphHollow) == lipgloss.Width(core.GlyphHollow) {
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
	m := seedWidget(core.WidgetSwitcher, 32, 12)
	next, _ := m.Update(coretest.Kr("j"))
	m = next.(Model)
	if m.selected != 1 {
		t.Fatalf("j should move the selection down, got %d", m.selected)
	}
	next, _ = m.Update(coretest.Kr("k"))
	m = next.(Model)
	if m.selected != 0 {
		t.Fatalf("k should move the selection up, got %d", m.selected)
	}
	// The selection never leaves the list.
	next, _ = m.Update(coretest.Kr("k"))
	if next.(Model).selected != 0 {
		t.Fatalf("selection should clamp at the first group")
	}

	next, cmd := m.Update(coretest.Kr("q"))
	if !next.(Model).quitting || cmd == nil {
		t.Fatalf("q should quit the widget")
	}
}

// TestStatusbarRendersOneLine pins the shape a one-row pane needs: exactly one
// line, exactly the pane width.
func TestStatusbarRendersOneLine(t *testing.T) {
	const w = 60
	m := seedWidget(core.WidgetStatusbar, w, 1)

	out := m.viewContent()
	if strings.Contains(out, "\n") {
		t.Fatalf("statusbar must render a single line, got:\n%q", out)
	}
	if got := lipgloss.Width(out); got != w {
		t.Errorf("statusbar width = %d, want %d", got, w)
	}
	// The left segment names the place, not the project (D44).
	for _, want := range []string{"/work/local-dev", "2 projects", "2 running"} {
		if !strings.Contains(out, want) {
			t.Errorf("statusbar should contain %q: %q", want, out)
		}
	}
	// A path is longer than the name it replaced, and 60 cells no longer hold
	// both it and the version: the bar's own rule is that where you are outranks
	// what version says it, so the version is what goes.
	if strings.Contains(out, "v0.0.0-test") {
		t.Errorf("a 60-cell bar has no room left for the version: %q", out)
	}
	if wide := seedWidget(core.WidgetStatusbar, 100, 1).viewContent(); !strings.Contains(
		wide, "v0.0.0-test",
	) {
		t.Errorf("a bar with room to spare should still carry the version: %q", wide)
	}

	// A pane too narrow for the version keeps where-you-are and still fills
	// its row exactly.
	narrow := seedWidget(core.WidgetStatusbar, 14, 1)
	line := narrow.viewContent()
	if got := lipgloss.Width(line); got != 14 {
		t.Errorf("narrow statusbar width = %d, want 14", got)
	}

	// The marker is the widest glyph there is when a bell is unread, and the
	// bar's padding is computed against it: a two-cell marker must not push the
	// row past its pane.
	next, _ := m.Update(core.CommandsLoadedMsg{Infos: []core.CommandInfo{{
		ID: "1", Name: "agent", Project: "local-dev", Workdir: "/work/local-dev",
		State: model.EventTypeRunning, BellUnread: true,
	}}})
	belled := next.(Model).viewContent()
	if !strings.Contains(belled, core.GlyphBell) {
		t.Errorf("an unread bell should reach the statusbar marker: %q", belled)
	}
	if got := lipgloss.Width(belled); got != w {
		t.Errorf("statusbar width with a bell = %d, want %d", got, w)
	}
}

// seedWidget builds a panel over two projects — local-dev is the cwd-tied one —
// driven through the same load messages the program delivers.
func seedWidget(w core.Widget, width, height int) Model {
	fb := &coretest.FakeBackend{
		Dir: "/work/local-dev",
		Cmds: []core.CommandInfo{
			{ID: "1", Name: "watcher", Project: "local-dev", Workdir: "/work/local-dev",
				State: model.EventTypeRunning},
			{ID: "2", Name: "seed-db", Project: "local-dev", Workdir: "/work/local-dev",
				State: model.EventTypeExited},
			{ID: "3", Name: "web", Project: "api-stack", Workdir: "/work/api",
				State: model.EventTypeRunning},
		},
		Projs: []core.ProjectInfo{
			{Name: "local-dev", Workdir: "/work/local-dev", Identity: "id-local-dev"},
			{Name: "api-stack", Workdir: "/work/api", Identity: "id-api-stack"},
		},
	}
	m := New(context.Background(), w, core.Options{Backend: fb, Version: "v0.0.0-test"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	next, _ = m.Update(core.CommandsLoadedMsg{Infos: fb.Cmds})
	m = next.(Model)
	next, _ = m.Update(core.ProjectsLoadedMsg{Infos: fb.Projs})
	return next.(Model)
}

// updWidget drives one message through the widget model.
func updWidget(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return got, cmd
}

// settle runs the command a gesture dispatched and delivers its message back,
// which is what the program loop does with it.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatalf("expected a command to run")
	}
	m, _ = updWidget(t, m, cmd())
	return m
}

// TestSwitcherSelectionSwitches covers the two selection gestures (D6/D24):
// enter on the cursor and a click on a row both take the client to that
// project's window, addressed by the identity the backend stamped on it.
func TestSwitcherSelectionSwitches(t *testing.T) {
	m := seedWidget(core.WidgetSwitcher, 32, 12)
	fb := m.backend.(*coretest.FakeBackend)

	m, cmd := updWidget(t, m, coretest.KEnter)
	m = settle(t, m, cmd)
	if !slices.Equal(fb.Switched, []string{"id-local-dev"}) {
		t.Fatalf("enter switched to %v, want the cwd-active project", fb.Switched)
	}
	if m.status != "" {
		t.Errorf("a switch that worked has nothing to say: %q", m.status)
	}

	// Row 4 is api-stack's command line: the title takes row 0, then local-dev's
	// head and its two commands. Clicking it is enter on that group.
	m, cmd = updWidget(t, m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 2, Y: 4})
	m = settle(t, m, cmd)
	if m.selected != 1 {
		t.Errorf("a click should move the cursor to the row it hit, selected = %d", m.selected)
	}
	if want := []string{"id-local-dev", "id-api-stack"}; !slices.Equal(fb.Switched, want) {
		t.Errorf("switched = %v, want %v", fb.Switched, want)
	}

	// The title row is chrome, and so is everything below the last group.
	for _, y := range []int{0, 6} {
		if _, cmd := updWidget(
			t,
			m,
			tea.MouseClickMsg{Button: tea.MouseLeft, X: 2, Y: y},
		); cmd != nil {
			t.Errorf("a click on row %d should hit no group", y)
		}
	}

	// A switch that fails says so where the hint line is.
	fb.SwitchErr = errors.New("no window is up for it")
	m, cmd = updWidget(t, m, coretest.KEnter)
	m = settle(t, m, cmd)
	if !strings.Contains(m.status, "no window is up for it") {
		t.Errorf("a failed switch should be reported, status = %q", m.status)
	}
}

// TestSwitcherSelectionNeedsIdentity pins the navigate-only boundary (V6) at
// its edge: a project the backend could not stamp an identity for has no window
// to go to, and saying so is all the switcher does about it — it never brings
// one up.
func TestSwitcherSelectionNeedsIdentity(t *testing.T) {
	m := seedWidget(core.WidgetSwitcher, 32, 12)
	fb := m.backend.(*coretest.FakeBackend)
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: nil})
	m, _ = updWidget(t, m, core.ProjectsLoadedMsg{Infos: []core.ProjectInfo{{Name: "never-run"}}})

	m, cmd := updWidget(t, m, coretest.KEnter)
	if cmd != nil {
		t.Fatalf("a project with no identity should dispatch nothing")
	}
	if !strings.Contains(m.status, "never-run") {
		t.Errorf("the switcher should name the project it cannot reach, status = %q", m.status)
	}
	if len(fb.Switched) != 0 {
		t.Errorf("backend was asked to switch to %v", fb.Switched)
	}
}

// TestSwitcherSelectionResolvesBells is D22's clear-on-selection: selecting a
// project through the switcher reads its bells. The acknowledgement lives in
// the widget because the monitor keeps reporting the bell unread — only an
// attach reads one there (D11) — so it has to survive the reload that follows,
// and only that bell: once the monitor's flag goes down, the next one is news.
func TestSwitcherSelectionResolvesBells(t *testing.T) {
	belled := []core.CommandInfo{{
		ID: "1", Name: "watcher", Project: "local-dev", Workdir: "/work/local-dev",
		State: model.EventTypeRunning, BellUnread: true,
	}}
	quiet := []core.CommandInfo{{
		ID: "1", Name: "watcher", Project: "local-dev", Workdir: "/work/local-dev",
		State: model.EventTypeRunning,
	}}

	m := seedWidget(core.WidgetSwitcher, 32, 12)
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: belled})
	if got := core.MarkerGlyph(m.groups[0]); got != core.GlyphBell {
		t.Fatalf("precondition: the marker should be the bell, got %q", got)
	}

	m, cmd := updWidget(t, m, coretest.KEnter)
	m = settle(t, m, cmd)
	if got := core.MarkerGlyph(m.groups[0]); got == core.GlyphBell {
		t.Errorf("selecting the project should have read its bell")
	}

	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: belled})
	if got := core.MarkerGlyph(m.groups[0]); got == core.GlyphBell {
		t.Errorf("the monitor's still-unread flag should not re-ring a read bell")
	}

	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: quiet})
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: belled})
	if got := core.MarkerGlyph(m.groups[0]); got != core.GlyphBell {
		t.Errorf("a bell that rang again is news again, marker = %q", got)
	}
}

// TestSwitcherCollapse covers V8's `z`: the docked switcher takes the whole
// frame down without leaving the keyboard. Whether the window was framed at all
// is the service's business — hide is a no-op there — so what this pins is that
// a hide reporting nothing stays quiet, and a failing one does not.
func TestSwitcherCollapse(t *testing.T) {
	m := seedWidget(core.WidgetSwitcher, 32, 12)
	fb := m.backend.(*coretest.FakeBackend)

	m, cmd := updWidget(t, m, coretest.Kr("z"))
	m = settle(t, m, cmd)
	if fb.Hidden != 1 {
		t.Fatalf("z should hide the frame once, got %d calls", fb.Hidden)
	}
	if m.status != "" {
		t.Errorf("an unframed window is a quiet no-op, status = %q", m.status)
	}

	fb.HideErr = errors.New("not inside a multiplexer")
	m, cmd = updWidget(t, m, coretest.Kr("z"))
	m = settle(t, m, cmd)
	if !strings.Contains(m.status, "not inside a multiplexer") {
		t.Errorf("a failed hide should be reported, status = %q", m.status)
	}

	// The statusbar has neither a cursor to select with nor a collapse gesture.
	sb := seedWidget(core.WidgetStatusbar, 60, 1)
	for _, key := range []tea.KeyMsg{coretest.Kr("z"), coretest.KEnter} {
		if _, cmd := updWidget(t, sb, key); cmd != nil {
			t.Errorf("the statusbar should ignore %v", key)
		}
	}
	if got := sb.backend.(*coretest.FakeBackend); got.Hidden != 0 || len(got.Switched) != 0 {
		t.Errorf("the statusbar dispatched %d hides and %v switches", got.Hidden, got.Switched)
	}
}

// TestWidgetNoQuit covers V6's flag: a widget docked in a frame pane must not
// exit from a keypress, and stops advertising a key it no longer has. A
// standalone run keeps quitting, which is what the flag is opt-in for.
func TestWidgetNoQuit(t *testing.T) {
	docked := seedWidget(core.WidgetSwitcher, 32, 12)
	docked.noQuit = true
	for _, key := range []tea.KeyMsg{
		coretest.Kr("q"),
		tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl},
	} {
		next, cmd := updWidget(t, docked, key)
		if next.quitting {
			t.Errorf("%v should not quit a docked widget", key)
		}
		if cmd != nil && coretest.MsgIsQuit(cmd()) {
			t.Errorf("%v should not produce Quit under --no-quit", key)
		}
	}
	if hint := docked.switcherFooter(); strings.Contains(hint, "q quit") {
		t.Errorf("a docked switcher should not hint at quitting: %q", hint)
	}

	standalone := seedWidget(core.WidgetSwitcher, 32, 12)
	next, cmd := updWidget(t, standalone, coretest.Kr("q"))
	if !next.quitting || cmd == nil || !coretest.MsgIsQuit(cmd()) {
		t.Errorf("q should still quit a standalone widget")
	}
	if hint := standalone.switcherFooter(); !strings.Contains(hint, "q quit") {
		t.Errorf("a standalone switcher should hint at quitting: %q", hint)
	}
}

func TestNewCarriesContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "threaded")
	m := New(ctx, core.WidgetSwitcher, core.Options{Backend: &coretest.FakeBackend{}})
	if m.ctx == nil || m.ctx.Value(ctxKey{}) == nil {
		t.Errorf("New should carry the caller's context into the model")
	}
}

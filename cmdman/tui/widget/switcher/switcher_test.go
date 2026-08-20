package switcher

import (
	"context"
	"errors"
	"fmt"
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
	groups := switcherGroups(projs, cmds, "/work/blog", "", nil)

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

// TestSwitcherGroupsActiveIdentityFirst is D3's mark: the window the caller sits
// in names the project, and the working directory only answers when no window
// did. The two disagree here on purpose — a popup or a docked pane runs wherever
// it was summoned from, which is exactly the case a cwd match gets wrong.
func TestSwitcherGroupsActiveIdentityFirst(t *testing.T) {
	projs := []core.ProjectInfo{
		{Name: "api", Workdir: "/work/api", Identity: "h1-api"},
		{Name: "blog", Workdir: "/work/blog", Identity: "h2-blog"},
	}

	for _, tc := range []struct {
		name     string
		cwd      string
		identity string
		want     string // name of the group expected to carry the mark, "" for none
	}{
		{"identity wins over an unrelated cwd", "/elsewhere", "h1-api", "api"},
		{"identity wins over a cwd naming another project", "/work/blog", "h1-api", "api"},
		{"no identity falls back to the cwd", "/work/blog", "", "blog"},
		{"an identity no listed project carries marks nothing", "/work/blog", "h9-gone", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups := switcherGroups(projs, nil, tc.cwd, tc.identity, nil)
			var marked []string
			for _, g := range groups {
				if g.Active {
					marked = append(marked, g.Name)
				}
			}
			var want []string
			if tc.want != "" {
				want = []string{tc.want}
			}
			if !slices.Equal(marked, want) {
				t.Errorf("marked active = %v, want %v", marked, want)
			}
		})
	}
}

// TestSwitcherActiveIdentityMsgRebuilds pins the wiring the mark rides on: the
// probe answers off the update loop, and its message is what re-marks the rows.
func TestSwitcherActiveIdentityMsgRebuilds(t *testing.T) {
	m := New(context.Background(), core.Options{
		Backend: &coretest.FakeBackend{Dir: "/elsewhere"},
	})
	next, _ := m.Update(core.ProjectsLoadedMsg{Infos: []core.ProjectInfo{
		{Name: "api", Workdir: "/work/api", Identity: "h1-api"},
	}})
	m = next.(Model)
	if g, _ := m.selectedGroup(); g.Active {
		t.Fatalf("nothing is active before a probe answers: %+v", g)
	}

	next, _ = m.Update(core.ActiveIdentityLoadedMsg{Identity: "h1-api", OK: true})
	m = next.(Model)
	if g, _ := m.selectedGroup(); !g.Active {
		t.Errorf("the probed project should be marked active: %+v", g)
	}

	// A later probe that does not answer must not leave the old claim standing.
	next, _ = m.Update(core.ActiveIdentityLoadedMsg{})
	m = next.(Model)
	if g, _ := m.selectedGroup(); g.Active {
		t.Errorf("an unanswered probe should drop back to cwd matching: %+v", g)
	}
}

// TestSwitcherRenderAlignment renders the grouped list and checks the property
// the marker/width math exists for: every group line is exactly the pane width,
// so the selected group is a solid block and nothing overflows the column.
func TestSwitcherRenderAlignment(t *testing.T) {
	const w, h = 32, 12
	m := seedWidget(t, w, h)

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
	full := seedWidget(t, w, 12)
	empty := seedWidget(t, w, 12)
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
	m := seedWidget(t, 60, 12)

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
	m := seedWidget(t, w, 12)
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
// particular, and one whose column is narrower than its path — where the
// disambiguating project name is budgeted out of the column before the path is
// cut to it, and dropped entirely once there is room for only one of the two.
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
		{
			// Cut as one string the suffix survives and the path is eaten
			// ("…(alpha)"), which names no place at all.
			name: "a tight column shortens the path, not the name it needs",
			g:    core.ProjectGroup{Name: "alpha", Workdir: "/work/shared"},
			dup:  true,
			w:    12,
			want: "…red (alpha)",
		},
		{
			name: "a column with room for one of the two keeps the path",
			g:    core.ProjectGroup{Name: "alpha", Workdir: "/work/shared"},
			dup:  true,
			w:    11,
			want: "…ork/shared",
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
	m := seedWidget(t, 24, 12)
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
// command that is one replica among several says which one — the index alone,
// never the count it was scaled to — and neither half of the unscaled zero
// value invents an answer. The listing already collapses a sole instance to
// that zero pair (cli.commandInfos), so what the guard here keeps out is a
// {1,1} pair reaching a row and badging an unscaled command.
func TestScaleBadgeMarksReplicasOnly(t *testing.T) {
	for _, tc := range []struct {
		name         string
		index, count int
		want         string
	}{
		{"one replica among several", 2, 3, " [2]"},
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

// TestSwitcherRowsNumberReplicas is the replica identity where it was asked
// for: a scaled command's row says which replica it is in its own fixed column,
// the identity survives the load path (core.GroupFromInfos) and a run that is
// over — D13 takes a command's words away, not what it is — and an unscaled row
// leaves the column blank rather than closing it up and shifting the title.
func TestSwitcherRowsNumberReplicas(t *testing.T) {
	const w = 60
	m := seedWidget(t, w, 12)
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

	for _, tc := range []struct{ id, want string }{
		{"1", " 2"},
		{"2", " 1"}, // the run is over; which replica it was is not something it said
		{"3", "  "},
	} {
		line := rowText(m, rowByID(t, m, tc.id), w)
		if got := rowColumn(line, rowIdxAt(w), core.GlyphWidth(rowIdx)); got != tc.want {
			t.Errorf("command %s index column = %q, want %q (%q)", tc.id, got, tc.want, line)
		}
	}
}

// TestSwitcherRowColumnsAlign is what the unconditional index and bell columns
// are for: the titles of a group's commands start at one column and can be read
// straight down, whether or not a row is one replica among several and whether
// or not it is ringing. Inserts drawn only on the rows that need them would move
// the title on exactly those rows.
func TestSwitcherRowColumnsAlign(t *testing.T) {
	const w = 60
	plain := core.CommandRow{
		ID: "1", Name: "web", State: model.EventTypeRunning, Title: "serving",
	}
	scaled, belled := plain, plain
	scaled.ScaleIndex, scaled.ScaleCount = 2, 3
	belled.Bell = true

	want := titleCol(t, rowText(Model{}, plain, w))
	for _, tc := range []struct {
		name string
		c    core.CommandRow
	}{
		{"a scaled row", scaled},
		{"a belled row", belled},
		{"a scaled row that is also ringing", func() core.CommandRow {
			c := scaled
			c.Bell = true
			return c
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := rowText(Model{}, tc.c, w)
			if got := titleCol(t, line); got != want {
				t.Errorf("the title starts at cell %d, want %d: %q", got, want, line)
			}
		})
	}
}

// TestSwitcherRowClampsName covers the name column's own cut: a name past it
// ends in an ellipsis saying there is more, rather than being hard cut into a
// word that reads as the whole name — and the columns after it do not move.
func TestSwitcherRowClampsName(t *testing.T) {
	const w = 60
	long := core.CommandRow{
		ID: "1", Name: "a-very-long-command-name", State: model.EventTypeRunning,
		Title: "serving",
	}
	short := long
	short.Name = "web"

	line := rowText(Model{}, long, w)
	if !strings.Contains(line, "a-very-long…") {
		t.Errorf("a name past its column should end in an ellipsis: %q", line)
	}
	if strings.Contains(line, "a-very-long-c") {
		t.Errorf("a name past its column must not be hard cut: %q", line)
	}
	if got, want := titleCol(t, line), titleCol(t, rowText(Model{}, short, w)); got != want {
		t.Errorf("a clamped name moved the title to cell %d, want %d: %q", got, want, line)
	}
}

// TestSwitcherRowClampsTitleToThePaneEdge is why the payload is cut by the row
// rather than left to core.PadLine: the line's own truncation ends a title
// mid-word with nothing saying so, while a title cut here says it was cut.
func TestSwitcherRowClampsTitleToThePaneEdge(t *testing.T) {
	c := core.CommandRow{
		ID: "1", Name: "web", State: model.EventTypeRunning,
		Title: "a title far longer than this pane could ever show",
	}
	for w := 20; w <= 40; w++ {
		line := rowText(Model{}, c, w)
		if got := core.Cells.StringWidth(line); got != w {
			t.Fatalf("row at width %d = %d cells: %q", w, got, line)
		}
		if !strings.HasSuffix(strings.TrimRight(line, " "), "…") {
			t.Errorf("a title cut at the pane edge should say so, width %d: %q", w, line)
		}
	}
}

// TestSwitcherRowPayloadForDeadRows is D13 in the title slot: a run that is over
// fills it with where in its life it stands — including an action already in
// flight, which outranks the state it is about to leave — and never with the
// last thing it said.
func TestSwitcherRowPayloadForDeadRows(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    core.CommandRow
		want string
	}{
		{
			name: "an exited command annotates its code",
			c: core.CommandRow{
				State: model.EventTypeExited, ExitCode: new(0), Title: "still talking",
			},
			want: "exited(0)",
		},
		{
			name: "a failed command says so",
			c:    core.CommandRow{State: model.EventTypeFailed},
			want: "failed",
		},
		{
			name: "an action in flight outranks the state it is leaving",
			c:    core.CommandRow{State: model.EventTypeRunning, Pending: "stopping"},
			want: "stopping…",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.ID, tc.c.Name = "1", "web"
			line := rowText(Model{}, tc.c, 60)
			if !strings.Contains(line, " · "+tc.want) {
				t.Errorf("a dead row should carry %q as its payload: %q", tc.want, line)
			}
			if strings.Contains(line, "still talking") {
				t.Errorf("a run that is over must not keep its last title (D13): %q", line)
			}
		})
	}
}

// TestSwitcherRowNameCarriesState is where the status word went: the row spends
// no column on it, so the name's own color has to say the same four things —
// and a run that is over wears none of them.
func TestSwitcherRowNameCarriesState(t *testing.T) {
	live := func(status string) core.CommandRow {
		return core.CommandRow{
			ID: "1", Name: "web", State: model.EventTypeRunning, Status: status,
		}
	}
	for _, tc := range []struct {
		name string
		c    core.CommandRow
		want lipgloss.Style
	}{
		{
			name: "a command waiting on the user is bold",
			c:    live(core.StatusWaiting),
			want: core.StyleMarkerBlocked.Bold(true),
		},
		{
			name: "a working command wears the marker's yellow",
			c:    live(core.StatusWorking),
			want: core.StyleMarkerWork,
		},
		{
			name: "a command that reported done wants nothing",
			c:    live(core.StatusDone),
			want: core.StyleMarkerIdle,
		},
		{
			name: "a live command that reported nothing at all is fainter",
			c:    live(""),
			want: core.StyleMarkerIdle.Faint(true),
		},
		{
			name: "a run that is over drops to the weak shade",
			c: func() core.CommandRow {
				c := live(core.StatusWaiting)
				c.State = model.EventTypeExited
				return c
			}(),
			want: core.StyleActive,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := core.RowNameStyle(tc.c, nil)
			if got.GetForeground() != tc.want.GetForeground() ||
				got.GetBold() != tc.want.GetBold() ||
				got.GetFaint() != tc.want.GetFaint() {
				t.Errorf("core.RowNameStyle() = %+v, want %+v", got, tc.want)
			}
			// The style is only half of it: the row has to be the thing wearing it.
			line := Model{}.commandLine(tc.c, core.BgNone, 60)
			name := tc.want.Render(rowIndent + core.PadCells(tc.c.Name, rowNameMax))
			if !strings.Contains(line, name) {
				t.Errorf("the row should render its name in the state's style: %q", line)
			}
		})
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

// TestStampTitleDatesPushArrival covers where the bucket times come from (L4):
// the monitor serves no title timestamp, so the push that carried a title is
// what dates it — and a push repeating the title a command already carries must
// keep the old stamp, or a talkative monitor would restamp everything into one
// bucket and the sort would say nothing.
func TestStampTitleDatesPushArrival(t *testing.T) {
	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	now := t0
	m := Model{now: func() time.Time { return now }, titles: map[string]titleStamp{}}

	m.stampTitle("1", "first")
	if got := m.titles["1"]; got.at != t0 || got.title != "first" {
		t.Fatalf("the first push's stamp = %+v, want first@%v", got, t0)
	}
	m.stampTitle("2", "")
	if len(m.titles) != 1 {
		t.Errorf("a command reporting no title should carry no stamp, got %v", m.titles)
	}

	now = t0.Add(30 * time.Second)
	m.stampTitle("1", "first")
	if got := m.titles["1"].at; got != t0 {
		t.Errorf("a repeated title should keep its stamp, got %v want %v", got, t0)
	}
	m.stampTitle("1", "retitled")
	if got := m.titles["1"].at; got != now {
		t.Errorf("a retitle should start a new bucket, got %v want %v", got, now)
	}

	// A monitor clearing the title (a restart resets the runtime state) takes the
	// command back below the ones that are saying something.
	m.stampTitle("1", "")
	if _, ok := m.titles["1"]; ok {
		t.Errorf("a cleared title should drop its stamp, got %v", m.titles)
	}
}

// TestSwitcherRowsShowRuntimeState pins what a command row says once the
// monitor answers: the title a live run set, its unread bell — and, for a run
// that is over, its exit state rather than any report (D13). The reported
// status is no longer a word of its own; it is the color the name wears, which
// TestSwitcherRowNameCarriesState covers.
func TestSwitcherRowsShowRuntimeState(t *testing.T) {
	m := seedWidget(t, 60, 12)
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
	for _, want := range []string{"agent", "review mon_run.go", core.GlyphBell} {
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

// --- live runtime state -----------------------------------------------------

// TestSwitcherInitArmsRuntimeWatch covers the arming hop: Init hands the merged
// receive over as a message, so the receive — which returns only when a monitor
// pushes — is started from an Update arm.
func TestSwitcherInitArmsRuntimeWatch(t *testing.T) {
	m := seedWidget(t, 32, 12)
	batch, ok := m.Init()().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init should batch its startup commands")
	}
	armed := false
	for _, c := range batch {
		if _, is := c().(runtimeWatchReadyMsg); is {
			armed = true
		}
	}
	if !armed {
		t.Fatalf("Init should arm the runtime-state receive")
	}
	if _, cmd := updWidget(t, m, runtimeWatchReadyMsg{}); cmd == nil {
		t.Fatalf("the arming message should start the merged receive")
	}
}

// TestLoadSubscribesLiveCommands pins the wiring the pushes arrive over: a load
// subscribes the commands whose monitors serve a stream and nobody else, and
// quitting hands those monitors their disconnects.
func TestLoadSubscribesLiveCommands(t *testing.T) {
	m := seedWidget(t, 32, 12)
	fb, ok := m.backend.(*coretest.FakeBackend)
	if !ok {
		t.Fatalf("seedWidget should carry the fake backend, got %T", m.backend)
	}
	// seed-db (id 2) has exited: it has no monitor left to dial.
	if want := []string{"1", "3"}; !slices.Equal(fb.WatchIDs, want) {
		t.Fatalf("subscribed %v, want %v", fb.WatchIDs, want)
	}
	stream := fb.WatchStreams["1"]
	if stream == nil {
		t.Fatalf("the running command should have been handed a stream")
	}

	m, _ = updWidget(t, m, coretest.Kr("q"))
	if !m.quitting {
		t.Fatalf("q should quit a standalone widget")
	}
	stream.WaitClosed(t)
}

// TestPushedRetitleMovesTheBucket is D20 fed by L4: the arrival of a retitle is
// what dates it, so a command that just said something floats above the ones
// that spoke a bucket ago — with no re-list anywhere in between.
func TestPushedRetitleMovesTheBucket(t *testing.T) {
	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	now := base
	m := seedWidget(t, 60, 12)
	m.now = func() time.Time { return now }
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: []core.CommandInfo{
		{ID: "1", Name: "alpha", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeRunning},
		{ID: "2", Name: "beta", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeRunning},
	}})

	// Both titled inside one bucket: there the order is the stable one, by name.
	m = pushState(t, m, "1", core.RuntimeStateView{Title: "building"})
	m = pushState(t, m, "2", core.RuntimeStateView{Title: "idle"})
	if got, want := rowNames(m.groups[0]), []string{"alpha", "beta"}; !slices.Equal(got, want) {
		t.Fatalf("commands titled in one bucket = %v, want %v", got, want)
	}

	now = base.Add(2 * titleBucket)
	m = pushState(t, m, "2", core.RuntimeStateView{Title: "running tests"})
	if got, want := rowNames(m.groups[0]), []string{"beta", "alpha"}; !slices.Equal(got, want) {
		t.Errorf("a pushed retitle should float its command up, got %v want %v", got, want)
	}
}

// TestPushedBellRespectsRead is D22 fed by pushes: the monitor keeps reporting
// a bell unread — only an attach reads one there (D11) — so a push must not
// re-ring the bell a selection already answered for, and must ring again once
// the monitor's own flag went down and the command rang anew.
func TestPushedBellRespectsRead(t *testing.T) {
	m := seedWidget(t, 32, 12)
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: []core.CommandInfo{
		{ID: "1", Name: "watcher", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeRunning},
	}})

	m = pushState(t, m, "1", core.RuntimeStateView{BellUnread: true})
	if got := core.MarkerGlyph(m.groups[0]); got != core.GlyphBell {
		t.Fatalf("precondition: a pushed bell should take the marker slot, got %q", got)
	}

	m, cmd := updWidget(t, m, coretest.KEnter)
	m = settle(t, m, cmd)
	if got := core.MarkerGlyph(m.groups[0]); got == core.GlyphBell {
		t.Fatalf("selecting the project should have read its bell")
	}

	m = pushState(t, m, "1", core.RuntimeStateView{BellUnread: true})
	if got := core.MarkerGlyph(m.groups[0]); got == core.GlyphBell {
		t.Errorf("the monitor's still-unread flag should not re-ring a read bell")
	}

	m = pushState(t, m, "1", core.RuntimeStateView{})
	m = pushState(t, m, "1", core.RuntimeStateView{BellUnread: true})
	if got := core.MarkerGlyph(m.groups[0]); got != core.GlyphBell {
		t.Errorf("a bell that rang again is news again, marker = %q", got)
	}
}

// TestRelistKeepsPushedState is what the cache is for: a list load carries no
// runtime state at all (L3), so without it every re-list would flash the rows
// back to empty titles until each monitor pushed again.
func TestRelistKeepsPushedState(t *testing.T) {
	m := seedWidget(t, 60, 12)
	fb := m.backend.(*coretest.FakeBackend)
	m = pushState(t, m, "1", core.RuntimeStateView{
		Title: "make build", Status: core.StatusWorking, Detail: "step 2/3",
	})

	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: seedInfos()})
	row := rowByID(t, m, "1")
	if row.Title != "make build" || row.Status != core.StatusWorking {
		t.Errorf("a re-list should keep the pushed state, got %+v", row)
	}
	if want := []string{"1", "3"}; !slices.Equal(fb.WatchIDs, want) {
		t.Errorf("a held stream should not be redialed, subscribed %v", fb.WatchIDs)
	}

	// The command exits: its stream is dropped, and what it said goes with it —
	// a run that is over says nothing (D13), including to the next run.
	infos := seedInfos()
	infos[0].State = model.EventTypeExited
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: infos})
	if len(m.runtime) != 0 || len(m.titles) != 0 {
		t.Errorf("an exited command should keep nothing: %v / %v", m.runtime, m.titles)
	}
	if row := rowByID(t, m, "1"); row.Title != "" {
		t.Errorf("an exited command should not keep its last title, got %q", row.Title)
	}
}

// TestPushForUnwatchedCommandIgnored covers the stragglers: an update the
// merged channel buffered before its stream was dropped, and one for a command
// this widget never listed at all. Neither may be cached — the sweep that
// forgets them has already run — and both keep the receive armed.
func TestPushForUnwatchedCommandIgnored(t *testing.T) {
	m := seedWidget(t, 60, 12)
	for _, id := range []string{"ghost", "2"} {
		m = pushState(t, m, id, core.RuntimeStateView{Title: "stale"})
	}
	if len(m.runtime) != 0 || len(m.titles) != 0 {
		t.Errorf("an ignored push should leave nothing behind: %v / %v", m.runtime, m.titles)
	}
	if row := rowByID(t, m, "2"); row.Title != "" {
		t.Errorf("an exited command's row should not take a straggler, got %q", row.Title)
	}
}

// TestRuntimeStreamEndAndErrorStayQuiet is criterion 5 at the widget: a monitor
// that died or stopped answering leaves the row as it was and says nothing,
// while the merged receive stays armed for every other command. Only the
// watcher's own close — which carries no id — ends the loop.
func TestRuntimeStreamEndAndErrorStayQuiet(t *testing.T) {
	m := seedWidget(t, 32, 12)
	for _, msg := range []core.RuntimeUpdateMsg{
		{ID: "1", Closed: true},
		{ID: "1", Err: errors.New("monitor went away")},
	} {
		next, cmd := updWidget(t, m, msg)
		if cmd == nil {
			t.Fatalf("%+v should keep the merged receive armed", msg)
		}
		m = next
		if m.status != "" {
			t.Errorf("a broken stream should not reach the hint line, got %q", m.status)
		}
	}
	if _, cmd := updWidget(t, m, core.RuntimeUpdateMsg{Closed: true}); cmd != nil {
		t.Fatalf("a closed watcher should stop the receive loop")
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
	m := seedWidget(t, 32, 12)
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

// TestSwitcherWheelScrollsTheView checks the wheel's half of the contract: a
// notch moves the visible window and leaves the cursor where it is, so the list
// can be looked through without the selection following the pointer around.
func TestSwitcherWheelScrollsTheView(t *testing.T) {
	m := scrollWidget(t, 32, 8)
	if got := paneOff(m); got != 0 {
		t.Fatalf("the list starts scrolled to %d, want the top", got)
	}

	m, _ = updWidget(t, m, wheelMsg(tea.MouseWheelDown))
	if m.selected != 0 {
		t.Errorf("the wheel moved the selection to %d; it scrolls away from the cursor",
			m.selected)
	}
	if got := paneOff(m); got != switcherWheelStep {
		t.Fatalf("one notch scrolled to line %d, want %d", got, switcherWheelStep)
	}
	out := paneText(m)
	if strings.Contains(out, "/work/proj-0") {
		t.Errorf("the group the notch scrolled past is still drawn:\n%s", out)
	}
	if !strings.Contains(out, "/work/proj-4") {
		t.Errorf("the rows the notch brought up are not drawn:\n%s", out)
	}
}

// TestSwitcherWheelClampsAtBothEnds checks that the view stops at the ends of
// the list: notches past either end are spent, not banked, so a notch the other
// way turns the view around at once.
func TestSwitcherWheelClampsAtBothEnds(t *testing.T) {
	m := scrollWidget(t, 32, 8)
	g := m.switcherGeometry(m.size())
	bottom := len(g.lines) - g.avail
	if bottom <= switcherWheelStep {
		t.Fatalf("the fixture nearly fits its pane: %d lines in %d rows",
			len(g.lines), g.avail)
	}

	m, _ = updWidget(t, m, wheelMsg(tea.MouseWheelUp))
	if got := paneOff(m); got != 0 {
		t.Errorf("a notch up at the top scrolled to line %d, want the top", got)
	}

	for range len(g.lines) {
		m, _ = updWidget(t, m, wheelMsg(tea.MouseWheelDown))
	}
	if got := paneOff(m); got != bottom {
		t.Fatalf("scrolling to the end landed on line %d, want %d", got, bottom)
	}
	if out := paneText(m); !strings.Contains(out, "/work/proj-7") {
		t.Errorf("the last group is not drawn at the end of the list:\n%s", out)
	}
	m, _ = updWidget(t, m, wheelMsg(tea.MouseWheelDown))
	if got := paneOff(m); got != bottom {
		t.Errorf("a notch past the end scrolled to line %d, want %d", got, bottom)
	}
	m, _ = updWidget(t, m, wheelMsg(tea.MouseWheelUp))
	if got, want := paneOff(m), bottom-switcherWheelStep; got != want {
		t.Errorf("a notch back up scrolled to line %d, want %d: the offset ran past the end",
			got, want)
	}
}

// TestSwitcherKeyMoveSnapsTheViewBack checks the other half of the contract: the
// next keyboard move hands the view back to the selection, wherever the wheel
// had left it.
func TestSwitcherKeyMoveSnapsTheViewBack(t *testing.T) {
	m := scrollWidget(t, 32, 8)
	for range 3 {
		m, _ = updWidget(t, m, wheelMsg(tea.MouseWheelDown))
	}
	if paneOff(m) == 0 {
		t.Fatalf("the wheel left the view at the top; nothing to snap back from")
	}

	m, _ = updWidget(t, m, coretest.Kr("j"))
	if m.selected != 1 {
		t.Fatalf("j should move the selection down, got %d", m.selected)
	}
	if m.scrolled {
		t.Errorf("a keyboard move should stop the view following the wheel")
	}
	if got := paneOff(m); got != 0 {
		t.Fatalf("the view stayed on line %d, want the selection's own offset 0", got)
	}
	if out := paneText(m); !strings.Contains(out, "/work/proj-1") {
		t.Errorf("the selected group is not on screen:\n%s", out)
	}

	for range 3 {
		m, _ = updWidget(t, m, wheelMsg(tea.MouseWheelDown))
	}
	m, _ = updWidget(t, m, coretest.Kr("k"))
	if m.selected != 0 || paneOff(m) != 0 {
		t.Fatalf("k left the selection on %d and the view on line %d, want 0 and 0",
			m.selected, paneOff(m))
	}
}

// TestSwitcherClickFollowsTheScrolledView checks that a click after a scroll
// lands on the row actually under the pointer. The render and the hit-test read
// one geometry, so the wheel moves both together.
func TestSwitcherClickFollowsTheScrolledView(t *testing.T) {
	m := scrollWidget(t, 32, 8)
	const y = 2 // the second list row, under the pinned title

	before, ok := m.groupAt(y)
	if !ok {
		t.Fatalf("row %d resolved to no group before the scroll", y)
	}
	m, _ = updWidget(t, m, wheelMsg(tea.MouseWheelDown))
	after, ok := m.groupAt(y)
	if !ok {
		t.Fatalf("row %d resolved to no group after the scroll", y)
	}
	if after == before {
		t.Fatalf("group %d is still under row %d; the hit-test did not follow the view",
			before, y)
	}
	g := m.switcherGeometry(m.size())
	if want := g.lines[g.off+y-g.top()].group; after != want {
		t.Fatalf("row %d resolves to group %d, the row drawn there belongs to %d",
			y, after, want)
	}

	want := m.groups[after].Name
	m, cmd := updWidget(t, m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 2, Y: y})
	m = settle(t, m, cmd)
	fb := m.backend.(*coretest.FakeBackend)
	if len(fb.Switched) != 1 || fb.Switched[0].Project != want {
		t.Fatalf("the click switched to %v, want the project drawn on row %d (%q)",
			fb.Switched, y, want)
	}
}

// wheelMsg is one notch of the wheel over the widget. The switcher is a single
// column, so where across it the pointer sits does not pick what scrolls.
func wheelMsg(b tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{Button: b, X: 1, Y: 1}
}

// paneOff is the list line the view is scrolled to as the widget stands.
func paneOff(m Model) int { return m.switcherGeometry(m.size()).off }

// paneText is what the widget draws, stripped of styling.
func paneText(m Model) string { return core.StripANSI(m.viewContent()) }

// seedInfos is the command list seedWidget loads: two live commands and one
// that has exited. It is rebuilt per call, so a test that edits an entry cannot
// reach the fixture the fake backend answers a re-list with.
func seedInfos() []core.CommandInfo {
	return []core.CommandInfo{
		{ID: "1", Name: "watcher", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeRunning},
		{ID: "2", Name: "seed-db", Project: "local-dev", Workdir: "/work/local-dev",
			State: model.EventTypeExited},
		{ID: "3", Name: "web", Project: "api-stack", Workdir: "/work/api",
			State: model.EventTypeRunning},
	}
}

// seedWidget builds a switcher over two projects — local-dev is the cwd-tied
// one — driven through the same load messages the program delivers. The load
// subscribes the live commands' runtime-state streams, so the watcher is torn
// down with the test rather than left pumping.
func seedWidget(t *testing.T, width, height int) Model {
	t.Helper()
	fb := &coretest.FakeBackend{
		Dir:  "/work/local-dev",
		Cmds: seedInfos(),
		Projs: []core.ProjectInfo{
			{
				Name: "local-dev", Workdir: "/work/local-dev",
				Path: "/work/local-dev/cmd-compose.yaml", Identity: "id-local-dev",
			},
			{
				Name: "api-stack", Workdir: "/work/api",
				Path: "/work/api/cmd-compose.yaml", Identity: "id-api-stack",
			},
		},
	}
	m := New(t.Context(), core.Options{Backend: fb})
	t.Cleanup(func() { _ = m.watcher.Close() })
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	next, _ = m.Update(core.CommandsLoadedMsg{Infos: fb.Cmds})
	m = next.(Model)
	next, _ = m.Update(core.ProjectsLoadedMsg{Infos: fb.Projs})
	return next.(Model)
}

// scrollWidget builds a switcher over more projects than its pane can show:
// eight groups of one command each, so the list is sixteen lines and the view
// has slack to scroll in both directions. Every command has exited, which keeps
// the runtime watcher out of a fixture that is only about layout.
//
// The groups are drawn in name order — proj-0 leads on its own name as well as
// on being the cwd-active one — which is what the tests' line arithmetic reads.
func scrollWidget(t *testing.T, width, height int) Model {
	t.Helper()
	const n = 8
	projs := make([]core.ProjectInfo, 0, n)
	cmds := make([]core.CommandInfo, 0, n)
	for i := range n {
		name := fmt.Sprintf("proj-%d", i)
		dir := "/work/" + name
		projs = append(projs, core.ProjectInfo{
			Name: name, Workdir: dir,
			Path: dir + "/cmd-compose.yaml", Identity: "id-" + name,
		})
		cmds = append(cmds, core.CommandInfo{
			ID: name, Name: "run", Project: name, Workdir: dir,
			State: model.EventTypeExited,
		})
	}
	fb := &coretest.FakeBackend{Dir: "/work/proj-0", Cmds: cmds, Projs: projs}
	m := New(t.Context(), core.Options{Backend: fb})
	t.Cleanup(func() { _ = m.watcher.Close() })
	m, _ = updWidget(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: cmds})
	m, _ = updWidget(t, m, core.ProjectsLoadedMsg{Infos: projs})
	return m
}

// rowByID returns the rendered row for a command id.
func rowByID(t *testing.T, m Model, id string) core.CommandRow {
	t.Helper()
	for _, g := range m.groups {
		for _, c := range g.Commands {
			if c.ID == id {
				return c
			}
		}
	}
	t.Fatalf("no row for command %q", id)
	return core.CommandRow{}
}

// rowText renders one command's row at pane width w the way switcherLines does,
// stripped of styling so its columns can be read as plain text.
func rowText(m Model, c core.CommandRow, w int) string {
	return core.StripANSI(core.PadLine(m.commandLine(c, core.BgNone, w), w, core.BgNone))
}

// rowColumn is cells [at, at+n) of a rendered, ANSI-stripped row. Cells, not
// bytes: the bell is two cells of four bytes, so a byte offset would put the
// belled rows' columns somewhere else than the quiet ones'.
func rowColumn(line string, at, n int) string {
	head := core.Cells.Truncate(line, at, "")
	return core.Cells.Truncate(strings.TrimPrefix(line, head), n, "")
}

// rowIdxAt is the cell a row's replica-index column starts at.
func rowIdxAt(w int) int { return core.GlyphWidth(rowIndent) + rowNameW(w) + 1 }

// titleCol is the cell a row's payload separator starts at — where a reader
// scanning a group's titles expects every one of them to begin.
func titleCol(t *testing.T, line string) int {
	t.Helper()
	head, _, ok := strings.Cut(line, rowSep)
	if !ok {
		t.Fatalf("row carries no payload: %q", line)
	}
	return core.Cells.StringWidth(head)
}

// rowNames lists a group's commands in the order they are drawn in.
func rowNames(g core.ProjectGroup) []string {
	out := make([]string, 0, len(g.Commands))
	for _, c := range g.Commands {
		out = append(out, c.Name)
	}
	return out
}

// pushState delivers one runtime-state push as the watcher's merged receive
// would, without going through a stream: the model arm is what is under test.
func pushState(t *testing.T, m Model, id string, v core.RuntimeStateView) Model {
	t.Helper()
	m, cmd := updWidget(t, m, core.RuntimeUpdateMsg{ID: id, State: v})
	if cmd == nil {
		t.Fatalf("a runtime update should rearm the receive")
	}
	return m
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
// project's window, addressed by the identity the backend stamped on it and
// described well enough — work directory and project name — for the backend to
// build that window when none is up.
func TestSwitcherSelectionSwitches(t *testing.T) {
	m := seedWidget(t, 32, 12)
	fb := m.backend.(*coretest.FakeBackend)

	localDev := core.SwitchTarget{
		Identity: "id-local-dev", WorkDir: "/work/local-dev", Project: "local-dev",
	}
	apiStack := core.SwitchTarget{
		Identity: "id-api-stack", WorkDir: "/work/api", Project: "api-stack",
	}

	m, cmd := updWidget(t, m, coretest.KEnter)
	m = settle(t, m, cmd)
	if !slices.Equal(fb.Switched, []core.SwitchTarget{localDev}) {
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
	if want := []core.SwitchTarget{localDev, apiStack}; !slices.Equal(fb.Switched, want) {
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
	fb.SwitchErr = errors.New("no multiplexer server is running")
	m, cmd = updWidget(t, m, coretest.KEnter)
	m = settle(t, m, cmd)
	if !strings.Contains(m.status, "no multiplexer server is running") {
		t.Errorf("a failed switch should be reported, status = %q", m.status)
	}
}

// TestSwitcherSelectionNeedsIdentity pins the one project a selection cannot
// reach: with no identity stamped there is no window to find and none to create
// under, so saying so on the hint line is all the switcher does about it.
func TestSwitcherSelectionNeedsIdentity(t *testing.T) {
	m := seedWidget(t, 32, 12)
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

	m := seedWidget(t, 32, 12)
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

// TestSwitcherSummonsProjectManager covers D7/D9's `m`: the panel opens over
// the project the cursor is on, named outright — file, work directory and all
// — so nothing about the window the popup lands in can redirect it (D17/D20).
// Where there is no floating pane to open, the reason takes the hint line (D4).
func TestSwitcherSummonsProjectManager(t *testing.T) {
	m := seedWidget(t, 32, 12)
	fb := m.backend.(*coretest.FakeBackend)
	if hint := m.switcherFooter(); !strings.Contains(hint, "m manage") {
		t.Errorf("the switcher should hint at the summon: %q", hint)
	}

	m, cmd := updWidget(t, m, coretest.Kr("m"))
	m = settle(t, m, cmd)
	want := []coretest.SummonCall{
		{
			Project: "local-dev",
			Path:    "/work/local-dev/cmd-compose.yaml",
			Workdir: "/work/local-dev",
		},
	}
	if !slices.Equal(fb.Summoned, want) {
		t.Fatalf("m summoned %v, want %v", fb.Summoned, want)
	}
	if m.status != "" {
		t.Errorf("a popup that ran has nothing to say: %q", m.status)
	}

	// The cursor addresses a whole group, head line and command rows alike, so
	// moving it moves what m manages.
	m, _ = updWidget(t, m, coretest.Kr("j"))
	m, cmd = updWidget(t, m, coretest.Kr("m"))
	m = settle(t, m, cmd)
	want = append(want, coretest.SummonCall{
		Project: "api-stack", Path: "/work/api/cmd-compose.yaml", Workdir: "/work/api",
	})
	if !slices.Equal(fb.Summoned, want) {
		t.Fatalf("m after j summoned %v, want %v", fb.Summoned, want)
	}

	fb.SummonErr = errors.New("popup driver \"zellij\" is not implemented yet")
	m, cmd = updWidget(t, m, coretest.Kr("m"))
	m = settle(t, m, cmd)
	if !strings.Contains(m.status, "not implemented") ||
		!strings.Contains(m.status, "api-stack") {
		t.Errorf("an unavailable popup should be reported inline, status = %q", m.status)
	}
	// It lands where the hints were: the column is narrow, so what fits of it is
	// the head of the line rather than the tail.
	if !strings.Contains(m.renderSwitcher(m.size()), "manage api-stack") {
		t.Errorf("the reason belongs where the hint line is:\n%q",
			m.renderSwitcher(m.size()))
	}
}

// TestSwitcherSummonNeedsAName pins the one row m cannot act on: a group the
// project listing never claimed has only its commands' project name, and
// without even that there is nothing to hand the child's command line.
func TestSwitcherSummonNeedsAName(t *testing.T) {
	m := seedWidget(t, 32, 12)
	fb := m.backend.(*coretest.FakeBackend)
	m, _ = updWidget(t, m, core.ProjectsLoadedMsg{Infos: nil})
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: []core.CommandInfo{
		{ID: "1", Name: "solo", Workdir: "/work/solo", State: model.EventTypeRunning},
	}})
	m.groups = []core.ProjectGroup{{Workdir: "/work/solo"}}

	m, cmd := updWidget(t, m, coretest.Kr("m"))
	if cmd != nil {
		t.Fatal("an unnamed group should dispatch nothing")
	}
	if !strings.Contains(m.status, "no project to manage") {
		t.Errorf("the switcher should say why, status = %q", m.status)
	}
	if len(fb.Summoned) != 0 {
		t.Errorf("backend was asked to summon %v", fb.Summoned)
	}
}

// TestSwitcherMuxDown covers `d`: the selected project's dashboard windows go
// away and its commands stay up, so the widget names the project — file and
// directory with it — and reports what came back. Nothing supervised is lost,
// which is why the key acts on the first press.
func TestSwitcherMuxDown(t *testing.T) {
	m := seedWidget(t, 32, 12)
	fb := m.backend.(*coretest.FakeBackend)
	if hint := m.switcherFooter(); !strings.Contains(hint, "d/D down") {
		t.Errorf("the switcher should hint at the teardowns: %q", hint)
	}

	m, cmd := updWidget(t, m, coretest.Kr("d"))
	m = settle(t, m, cmd)
	want := []coretest.DownCall{{
		Project: "local-dev",
		Path:    "/work/local-dev/cmd-compose.yaml",
		Workdir: "/work/local-dev",
	}}
	if !slices.Equal(fb.MuxDowns, want) {
		t.Fatalf("d tore down %v, want %v", fb.MuxDowns, want)
	}
	if len(fb.ComposeDowns) != 0 {
		t.Fatalf("d must leave the commands alone, compose downs = %v", fb.ComposeDowns)
	}
	if !strings.Contains(m.status, "commands still running") {
		t.Errorf("d should say what it left up, status = %q", m.status)
	}

	// A project whose spec has no mux section is the everyday refusal, and the
	// key stays on offer for it: nothing in the listing says which projects have
	// a dashboard.
	fb.MuxDownErr = errors.New("project \"local-dev\" has no mux: section")
	m, cmd = updWidget(t, m, coretest.Kr("d"))
	m = settle(t, m, cmd)
	if !strings.Contains(m.status, "no mux: section") {
		t.Errorf("a refused teardown should be reported, status = %q", m.status)
	}
	if !strings.Contains(m.renderSwitcher(m.size()), "mux down") {
		t.Errorf("the reason belongs where the hint line is:\n%q", m.renderSwitcher(m.size()))
	}
}

// TestSwitcherComposeDownConfirms covers `D`: it takes away every command of the
// project, so the first press only asks. y goes ahead and reports what the
// teardown did; any other key takes the question back and is spent doing so.
func TestSwitcherComposeDownConfirms(t *testing.T) {
	m := seedWidget(t, 60, 12)
	fb := m.backend.(*coretest.FakeBackend)
	fb.ComposeDownSummary = core.DownSummary{Stopped: 3, Removed: 2}

	m, cmd := updWidget(t, m, coretest.Kr("D"))
	if cmd != nil {
		t.Fatalf("D on its own should dispatch nothing")
	}
	if len(fb.ComposeDowns) != 0 {
		t.Fatalf("D on its own tore down %v", fb.ComposeDowns)
	}
	if got := core.StripANSI(m.switcherFooter()); !strings.Contains(
		got, "compose down local-dev? y/n") {
		t.Fatalf("D should ask before it acts, footer = %q", got)
	}

	cancelled, cmd := updWidget(t, m, coretest.Kr("j"))
	if cmd != nil || len(fb.ComposeDowns) != 0 {
		t.Fatalf("a key that is not y should tear nothing down, %v", fb.ComposeDowns)
	}
	if cancelled.selected != 0 {
		t.Errorf("the key that cancelled should not also move the cursor, selected = %d",
			cancelled.selected)
	}
	if !strings.Contains(cancelled.status, "cancelled") {
		t.Errorf("a cancelled teardown should say so, status = %q", cancelled.status)
	}
	// With the question answered the same key is a movement key again.
	cancelled, _ = updWidget(t, cancelled, coretest.Kr("j"))
	if cancelled.selected != 1 {
		t.Errorf("j after the question should move again, selected = %d", cancelled.selected)
	}

	m, cmd = updWidget(t, m, coretest.Kr("y"))
	m = settle(t, m, cmd)
	want := []coretest.DownCall{{
		Project: "local-dev",
		Path:    "/work/local-dev/cmd-compose.yaml",
		Workdir: "/work/local-dev",
	}}
	if !slices.Equal(fb.ComposeDowns, want) {
		t.Fatalf("y tore down %v, want %v", fb.ComposeDowns, want)
	}
	if len(fb.MuxDowns) != 0 {
		t.Errorf("D must not touch the dashboard, mux downs = %v", fb.MuxDowns)
	}
	if !strings.Contains(m.status, "stopped 3, removed 2") {
		t.Errorf("the teardown should report what it did, status = %q", m.status)
	}
}

// TestSwitcherComposeDownReportsPartialTeardown pins the half a non-nil error
// must not hide: a teardown that failed on one command still tore the others
// down, and both belong on the status line.
func TestSwitcherComposeDownReportsPartialTeardown(t *testing.T) {
	m := seedWidget(t, 60, 12)
	fb := m.backend.(*coretest.FakeBackend)
	fb.ComposeDownSummary = core.DownSummary{Stopped: 2, Removed: 1}
	fb.ComposeDownErr = errors.New("remove seed-db: still running")

	m, _ = updWidget(t, m, coretest.Kr("D"))
	m, cmd := updWidget(t, m, coretest.Kr("y"))
	m = settle(t, m, cmd)
	if !strings.Contains(m.status, "stopped 2, removed 1") ||
		!strings.Contains(m.status, "remove seed-db: still running") {
		t.Errorf("a partial teardown should report both halves, status = %q", m.status)
	}
}

// TestSwitcherComposeDownPromptSurvivesAReload pins where the question lives: a
// listing lands on its own and clears the status line, and a question that went
// off the screen while the next key still answered it would take the commands
// of a project nobody was looking at.
func TestSwitcherComposeDownPromptSurvivesAReload(t *testing.T) {
	m := seedWidget(t, 60, 12)
	fb := m.backend.(*coretest.FakeBackend)

	m, _ = updWidget(t, m, coretest.Kr("D"))
	m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: seedInfos()})
	if got := core.StripANSI(m.switcherFooter()); !strings.Contains(
		got, "compose down local-dev? y/n") {
		t.Fatalf("the question should outlive a reload, footer = %q", got)
	}

	m, cmd := updWidget(t, m, coretest.Kr("y"))
	settle(t, m, cmd)
	if len(fb.ComposeDowns) != 1 {
		t.Errorf("y after the reload should still tear the project down, %v", fb.ComposeDowns)
	}
}

// TestSwitcherTeardownsNeedAName pins the row neither teardown can act on: both
// address the project by name, and a group the project listing never claimed a
// name for has nothing to hand them.
func TestSwitcherTeardownsNeedAName(t *testing.T) {
	for _, key := range []tea.KeyMsg{coretest.Kr("d"), coretest.Kr("D")} {
		m := seedWidget(t, 32, 12)
		fb := m.backend.(*coretest.FakeBackend)
		m, _ = updWidget(t, m, core.ProjectsLoadedMsg{Infos: nil})
		m, _ = updWidget(t, m, core.CommandsLoadedMsg{Infos: []core.CommandInfo{
			{ID: "1", Name: "solo", Workdir: "/work/solo", State: model.EventTypeRunning},
		}})
		m.groups = []core.ProjectGroup{{Workdir: "/work/solo"}}

		m, cmd := updWidget(t, m, key)
		if cmd != nil {
			t.Fatalf("%v on an unnamed group should dispatch nothing", key)
		}
		if !strings.Contains(m.status, "no project to tear down") {
			t.Errorf("%v should say why, status = %q", key, m.status)
		}
		if len(fb.MuxDowns) != 0 || len(fb.ComposeDowns) != 0 {
			t.Errorf("%v tore down %v / %v", key, fb.MuxDowns, fb.ComposeDowns)
		}
	}
}

// TestWidgetNoQuit covers V6's flag: a widget docked in a frame pane must not
// exit from a keypress, and stops advertising a key it no longer has. A
// standalone run keeps quitting, which is what the flag is opt-in for.
func TestWidgetNoQuit(t *testing.T) {
	docked := seedWidget(t, 32, 12)
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

	standalone := seedWidget(t, 32, 12)
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
	m := New(ctx, core.Options{Backend: &coretest.FakeBackend{}})
	if m.ctx == nil || m.ctx.Value(ctxKey{}) == nil {
		t.Errorf("New should carry the caller's context into the model")
	}
}

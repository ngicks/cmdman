package panel

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// switcherGroups joins the global project list (the ListProjects merge:
// store-known projects, never-run named defs, and the project discovered in the
// working directory) with the command list, so a project with no commands still
// gets a group and a running command lands under its project's head.
//
// Commands are matched on (workdir, project name) — both listings normalize the
// path the same way — falling back to the project name alone for a ProjectInfo
// carrying no workdir, which is how a never-run named def arrives. A command
// group no project entry claims is appended, so a project the listing missed is
// still on screen; standalone commands (no compose project) are dropped, having
// no project window to switch to.
func switcherGroups(
	projs []core.ProjectInfo,
	cmds []core.CommandInfo,
	cwd string,
	titles map[string]titleStamp,
) []core.ProjectGroup {
	cmdGroups := core.GroupFromInfos(cmds)
	claimed := make([]bool, len(cmdGroups))
	byKey := make(map[string]int, len(cmdGroups))
	byName := make(map[string][]int, len(cmdGroups))
	for i, g := range cmdGroups {
		if g.Name == "" {
			claimed[i] = true
			continue
		}
		byKey[g.Key()] = i
		byName[g.Name] = append(byName[g.Name], i)
	}

	out := make([]core.ProjectGroup, 0, len(projs)+len(cmdGroups))
	for _, p := range projs {
		g := core.ProjectGroup{Name: p.Name, Workdir: p.Workdir, Identity: p.Identity}
		if i, ok := matchCommandGroup(p, byKey, byName, claimed); ok {
			claimed[i] = true
			g.Commands = cmdGroups[i].Commands
			if g.Workdir == "" {
				g.Workdir = cmdGroups[i].Workdir
			}
		}
		out = append(out, g)
	}
	for i, g := range cmdGroups {
		if !claimed[i] {
			out = append(out, g)
		}
	}

	for i := range out {
		out[i].Active = out[i].Workdir != "" && out[i].Workdir == cwd
		bucketSort(out[i].Commands, titles)
	}
	slices.SortStableFunc(out, func(a, b core.ProjectGroup) int {
		if a.Active != b.Active {
			return core.BoolFirst(a.Active)
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// matchCommandGroup finds the command group belonging to a project entry: the
// exact (workdir, name) group, or — for an entry with no workdir — any
// unclaimed group of that name. An entry that carries a workdir never claims a
// group from another directory: same-named projects in different directories
// are different projects.
func matchCommandGroup(
	p core.ProjectInfo,
	byKey map[string]int,
	byName map[string][]int,
	claimed []bool,
) (int, bool) {
	if i, ok := byKey[p.Workdir+"\x00"+p.Name]; ok && !claimed[i] {
		return i, true
	}
	if p.Workdir != "" {
		return 0, false
	}
	for _, i := range byName[p.Name] {
		if !claimed[i] {
			return i, true
		}
	}
	return 0, false
}

// switcherLine is one rendered line of the scrollable region together with the
// group it belongs to, so the viewport can keep a whole group in view.
type switcherLine struct {
	text  string
	group int
}

// titleStamp is a command's current title and when it was first seen carrying
// it. The monitor serves no title timestamp, so "when the title changed" is
// observed here: each load compares the title it fetched against the one the
// last load saw. A title that arrived before the TUI started therefore dates
// from the first load, which puts every project's commands in one bucket until
// something actually retitles — the honest answer, since nothing else is known.
type titleStamp struct {
	title string
	at    time.Time
}

// titleBucket chunks title-update times (D20). Two agents retitling every few
// seconds land in the same bucket and order by name instead of trading places
// on every refresh.
const titleBucket = 5 * time.Second

// bucketSort orders a project's commands newest-title-bucket-first, then by
// name (then id) inside a bucket. Commands with no title sort last: "recently
// active floats up" says nothing about a command that never said anything.
func bucketSort(cmds []core.CommandRow, titles map[string]titleStamp) {
	slices.SortFunc(cmds, func(a, b core.CommandRow) int {
		return cmp.Or(
			cmp.Compare(titleBucketOf(titles[b.ID]), titleBucketOf(titles[a.ID])),
			cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.ID, b.ID),
		)
	})
}

// titleBucketOf is the bucket index of a stamp, with the zero stamp (no title
// seen) pinned below every real one rather than converted, since the zero
// time's Unix value is a large negative number that would merely look like one.
func titleBucketOf(s titleStamp) int64 {
	if s.at.IsZero() {
		return math.MinInt64
	}
	return s.at.UnixNano() / int64(titleBucket)
}

// renderSwitcher renders the docked column: a grouped list, each project
// heading its group with its one marker slot and its commands listed under it.
// The selected group is highlighted as one solid background block, head line
// and command rows together. The list scrolls inside the pane; the title and
// the hint footer are pinned, so only the group rows move.
//
// A def can dock a switcher into a pane of any width and height, so the chrome
// yields to the pane rather than overflowing it: the hint line goes first, then
// the title, and the group rows are the last thing standing. The two chrome
// lines are cut to the pane's width as well — a hint or an error longer than the
// column would wrap and cost the list a row it was not given.
func (m Model) renderSwitcher(w, h int) string {
	g := m.switcherGeometry(w, h)

	out := make([]string, 0, max(h, 1))
	if g.title {
		out = append(out, ansi.Truncate(core.StyleWidgetTitle.Render("projects"), w, ""))
	}
	out = append(out, linesText(g.lines[g.off:min(g.off+g.avail, len(g.lines))])...)
	if g.footer {
		out = append(out, ansi.Truncate(m.switcherFooter(), w, ""))
	}
	return strings.Join(out, "\n")
}

// switcherGeometry is where the docked column's rows land: which chrome fits,
// how many rows are left for the list, and where the list is scrolled to. The
// render and the click hit-test both read it, so a click resolves against the
// rows that were actually drawn rather than a second guess at the layout.
type switcherGeometry struct {
	lines         []switcherLine
	title, footer bool
	avail         int // rows the list may use
	off           int // index of the first visible line
}

// top is the screen row the first visible list line occupies.
func (g switcherGeometry) top() int {
	if g.title {
		return 1
	}
	return 0
}

func (m Model) switcherGeometry(w, h int) switcherGeometry {
	h = max(h, 1)
	g := switcherGeometry{title: h >= 2, footer: h >= 3, avail: h}
	if g.title {
		g.avail--
	}
	if g.footer {
		g.avail--
	}
	g.lines = m.switcherLines(w)
	if len(g.lines) == 0 {
		g.lines = []switcherLine{{text: core.StyleActive.Render("No projects."), group: -1}}
	}
	g.off = viewportOffset(g.lines, m.selected, g.avail)
	return g
}

// groupAt resolves a screen row to the group drawn on it. The chrome rows and
// the placeholder shown when there is nothing to list belong to no group.
func (m Model) groupAt(y int) (int, bool) {
	w, h := m.size()
	g := m.switcherGeometry(w, h)
	i := g.off + y - g.top()
	if y < g.top() || i >= min(g.off+g.avail, len(g.lines)) {
		return 0, false
	}
	group := g.lines[i].group
	if group < 0 || group >= len(m.groups) {
		return 0, false
	}
	return group, true
}

func linesText(lines []switcherLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.text
	}
	return out
}

// switcherFooter is the pinned last line: the transient error text when there
// is one, else the key hints. Quit is hinted only where it is bound — a docked
// switcher runs with it unbound (V6).
func (m Model) switcherFooter() string {
	if m.status != "" {
		return core.StyleActive.Render(m.status)
	}
	hint := "j/k move · enter switch · z hide"
	if !m.noQuit {
		hint += " · q quit"
	}
	return core.StyleActive.Render(hint)
}

// switcherLines renders the scrollable region: every project's head line
// followed by one line per command, each padded to w so a highlighted group
// forms a solid block.
func (m Model) switcherLines(w int) []switcherLine {
	var lines []switcherLine
	for i, g := range m.groups {
		bg := core.BgNone
		if i == m.selected {
			bg = core.BgAccent
		}
		lines = append(lines, switcherLine{text: core.PadLine(m.headLine(g, bg), w, bg), group: i})
		for _, c := range g.Commands {
			lines = append(
				lines,
				switcherLine{text: core.PadLine(m.commandLine(c, bg), w, bg), group: i},
			)
		}
	}
	return lines
}

// headLine is a project's head: its marker in a fixed-width slot — so heads
// line up with each other whichever marker shows — then its name. The gap comes
// off the glyph's measured width, not an assumed one, and the margin leads.
func (m Model) headLine(g core.ProjectGroup, bg core.RowBg) string {
	glyph := core.MarkerGlyph(g)
	gap := strings.Repeat(" ", max(core.MarkerSlot-core.GlyphWidth(glyph), 1))
	name := g.Name
	if name == "" {
		name = "(unnamed)"
	}
	head := bg.Plain(core.MarkerMargin) +
		bg.Style(core.MarkerStyle(g)).Render(glyph) +
		bg.Style(core.StyleWidgetHead).Render(gap+name)
	if g.Active {
		// Same word the rest of the TUI marks the cwd project with.
		head += bg.Style(core.StyleActive).Render("  active")
	}
	return head
}

// commandLine is one command under its project's head: the command name in the
// weak shade derived from the terminal's own colors, its state badge, an unread
// bell when it has one, and the title it last set — the signal the grouped list
// exists for (D20), fainter still than the name so a group reads as head plus
// detail.
func (m Model) commandLine(c core.CommandRow, bg core.RowBg) string {
	line := bg.Style(m.weakStyle()).Render("    "+core.PadCells(c.Name, 12)+" ") +
		core.RowStateBadge(c, bg)
	if !core.LiveReport(c) {
		// Nothing a finished run said still speaks for it (D13).
		return line
	}
	if c.Bell {
		line += bg.Plain(" " + core.GlyphBell)
	}
	if c.Title != "" {
		line += bg.Style(core.StyleActive).Render(" · " + c.Title)
	}
	return line
}

// viewportOffset scrolls the list so the selected group stays visible: as much
// of its block as fits, and its head line before its tail when the group is
// taller than the pane.
func viewportOffset(lines []switcherLine, selected, avail int) int {
	if len(lines) <= avail {
		return 0
	}
	start, end := -1, -1
	for i, l := range lines {
		if l.group != selected {
			continue
		}
		if start < 0 {
			start = i
		}
		end = i
	}
	if start < 0 {
		return 0
	}
	off := max(end-avail+1, 0)
	off = min(off, start)
	return min(off, len(lines)-avail)
}

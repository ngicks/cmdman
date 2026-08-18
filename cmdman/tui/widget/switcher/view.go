package switcher

import (
	"strings"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
	ansi "github.com/ngicks/cmdman/internal/third_party/charmbracelet-x-ansi"
)

// switcherLine is one rendered line of the scrollable region together with the
// group it belongs to, so the viewport can keep a whole group in view.
type switcherLine struct {
	text  string
	group int
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

// switcherFooter is the pinned last line: a teardown waiting to be confirmed
// first, since the next key answers it; then the transient error text; else the
// key hints. Quit is hinted only where it is bound — a docked switcher runs
// with it unbound (V6).
func (m Model) switcherFooter() string {
	if m.pendingDown.Project != "" {
		return core.StyleActive.Render(core.ComposeDownPrompt(m.pendingDown.Project))
	}
	if m.status != "" {
		return core.StyleActive.Render(m.status)
	}
	hint := "j/k move · enter switch · m manage · d/D down"
	if !m.noQuit {
		hint += " · q quit"
	}
	return core.StyleActive.Render(hint)
}

// switcherLines renders the scrollable region: every project's head line
// followed by one line per command, each padded to w so a highlighted group
// forms a solid block.
func (m Model) switcherLines(w int) []switcherLine {
	dup := sharedWorkdirs(m.groups)
	var lines []switcherLine
	for i, g := range m.groups {
		bg := core.BgNone
		if i == m.selected {
			bg = core.BgAccent
		}
		lines = append(lines, switcherLine{
			text:  core.PadLine(m.headLine(g, bg, dup[g.Workdir], w), w, bg),
			group: i,
		})
		for _, c := range g.Commands {
			lines = append(
				lines,
				switcherLine{text: core.PadLine(m.commandLine(c, bg), w, bg), group: i},
			)
		}
	}
	return lines
}

// sharedWorkdirs is the set of directories two or more listed groups sit in.
// Their heads name the directory (D44), which for those groups no longer says
// which project is which, so they — and only they — spell their project name
// out as well. A group with no directory at all heads with its name anyway.
func sharedWorkdirs(groups []core.ProjectGroup) map[string]bool {
	count := make(map[string]int, len(groups))
	for _, g := range groups {
		if g.Workdir == "" {
			continue
		}
		count[g.Workdir]++
	}
	dup := make(map[string]bool)
	for dir, n := range count {
		if n > 1 {
			dup[dir] = true
		}
	}
	return dup
}

// headActive is the word the rest of the TUI marks the project the user is in
// with (see activeMark), with the gap that sets it off from the head.
const headActive = "  active"

// headLine is a project's head: its marker in a fixed-width slot — so heads
// line up with each other whichever marker shows — then the directory it sits
// in. The gap comes off the glyph's measured width, not an assumed one, and the
// margin leads; what is left of w after them is what the path may spend.
//
// The active marker is reserved out of that budget rather than left to the
// row's own truncation: a path fills a docked column where a project name never
// did, and "you are here" losing to the tail of a path it could have shortened
// instead is the wrong trade.
func (m Model) headLine(g core.ProjectGroup, bg core.RowBg, dup bool, w int) string {
	glyph := core.MarkerGlyph(g)
	gap := strings.Repeat(" ", max(core.MarkerSlot-core.GlyphWidth(glyph), 1))
	avail := w - core.GlyphWidth(core.MarkerMargin) - core.GlyphWidth(glyph) - core.GlyphWidth(gap)
	if g.Active {
		avail -= core.GlyphWidth(headActive)
	}
	head := bg.Plain(core.MarkerMargin) +
		bg.Style(core.MarkerStyle(g)).Render(glyph) +
		bg.Style(core.StyleWidgetHead).Render(gap+headLabel(g, dup, max(avail, 1)))
	if g.Active {
		head += bg.Style(core.StyleActive).Render(headActive)
	}
	return head
}

// headMinPath is the least path a disambiguated head keeps: the ellipsis plus a
// few cells of where the directory ends. Narrower than that the project name is
// the cheaper thing to lose.
const headMinPath = 4

// headLabel is what a group's head calls itself: the directory it sits in
// (D44), home-abbreviated and cut to w cells keeping its tail — several compose
// projects can run on one directory, so the project name misidentifies the
// place, and the end of a path is what distinguishes it. A group sharing its
// directory with another visible group appends the project name to tell the two
// apart.
//
// A group with no directory — a named def that has never run anywhere in
// particular — has only its name to give, and a name is not a path: it is left
// for the row's own right-hand truncation, which keeps the head of it.
func headLabel(g core.ProjectGroup, dup bool, w int) string {
	if g.Workdir == "" {
		return groupLabel(g)
	}
	label := core.AbbrevHome(g.Workdir, core.HomeDir())
	if !dup {
		return core.TruncateLeftCells(label, w)
	}
	name := g.Name
	if name == "" {
		// The same word groupLabel falls back to, without its parentheses:
		// wrapped again the head would read "((unnamed))".
		name = "unnamed"
	}
	suffix := " (" + name + ")"
	// The suffix is reserved out of the budget rather than cut along with the
	// path: cut together it is the path that goes, leaving "…(alpha)" — a name
	// where the head's job is to say the place. A column with no room for both
	// drops the suffix instead, since the path is the identity and the name only
	// tells two of them apart.
	if room := w - core.GlyphWidth(suffix); room >= headMinPath {
		return core.TruncateLeftCells(label, room) + suffix
	}
	return core.TruncateLeftCells(label, w)
}

// commandLine is one command under its project's head: the command name in the
// weak shade derived from the terminal's own colors, its state badge, which
// replica it is when it is one of several (D44), an unread bell when it has
// one, and the title it last set — the signal the grouped list exists for
// (D20), fainter still than the name so a group reads as head plus detail.
func (m Model) commandLine(c core.CommandRow, bg core.RowBg) string {
	line := bg.Style(m.weakStyle()).Render("    "+core.PadCells(c.Name, 12)+" ") +
		core.RowStateBadge(c, bg)
	// Which replica this is comes before the report and outlives it: it is the
	// command's identity, not something it said, so a run that is over keeps it
	// where D13 takes its words away. An unscaled row appends nothing at all
	// rather than an empty render, which would still emit the style's escapes.
	if badge := core.ScaleBadge(c); badge != "" {
		line += bg.Style(m.weakStyle()).Render(badge)
	}
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

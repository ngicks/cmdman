package switcher

import (
	"strings"

	tea "charm.land/bubbletea/v2"

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
	if m.scrolled {
		// The wheel is driving, so the view is where it was left rather than where
		// the selection is (see wheel). Clamped here and not only at the notch: the
		// list the offset was taken against can have been reloaded or re-laid-out
		// since, and an offset past the end of a shorter list would draw nothing.
		g.off = clampOffset(m.scrollOff, len(g.lines), g.avail)
	} else {
		g.off = viewportOffset(g.lines, m.selected, g.avail)
	}
	return g
}

// switcherWheelStep is how far one wheel notch scrolls, the same notch the
// launcher's panes take.
const switcherWheelStep = 3

// wheel scrolls the list, cursor unmoved: the wheel looks around, and the keys
// are what act on what is found. It leaves a teardown waiting to be confirmed
// standing — unlike a click, it moves no cursor, so the question still names the
// project it was asked about.
//
// The notch is taken from where the list already sits rather than from a stored
// offset that may never have been set, which is what makes the first notch of a
// selection-following view move by a notch instead of jumping to the top.
func (m Model) wheel(msg tea.MouseWheelMsg) Model {
	d := switcherWheelStep
	switch msg.Button {
	case tea.MouseWheelUp:
		d = -switcherWheelStep
	case tea.MouseWheelDown:
	default:
		return m
	}
	g := m.switcherGeometry(m.size())
	m.scrollOff, m.scrolled = clampOffset(g.off+d, len(g.lines), g.avail), true
	return m
}

// clampOffset bounds a scroll offset to the list: never negative, and never past
// the last screenful — which a list that fits its pane does not have, so the
// upper bound falls back to the top rather than going negative.
func clampOffset(off, lines, avail int) int {
	return min(max(off, 0), max(lines-avail, 0))
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
	hint := "j/k move · enter switch · m/M manage · d/D down"
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
				switcherLine{text: core.PadLine(m.commandLine(c, bg, w), w, bg), group: i},
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

// The command row's fixed columns, as raw text so their widths are measured
// with the same ruler that draws them. The bell's width is not written down
// here for the reason MarkerSlot is not: a terminal that draws 🔔 at another
// width should move the columns rather than tear them apart.
const (
	rowIndent = "    "
	rowIdx    = "  " // core.ScaleCell's width
	rowSep    = " · "
)

// The name column's bounds. A name cut past rowNameMin identifies no row at
// all, and past rowNameMax it would spend on padding what the title — the
// signal the grouped list exists for (D20) — has better use for.
const (
	rowNameMin = 6
	rowNameMax = 12
	// rowTitleFloor is the payload the name column yields to before it starts
	// shrinking: a wide name is worth less than the first words of a title, and
	// a column that shows no title at all is a listing of names.
	rowTitleFloor = 8
)

// rowFixedW is every cell of a command row that is neither the name nor the
// payload: the indent, the two gaps, the replica index, the bell and the
// separator.
func rowFixedW() int {
	return core.GlyphWidth(rowIndent) + 1 + core.GlyphWidth(rowIdx) + 1 +
		core.GlyphWidth(core.GlyphBell) + core.GlyphWidth(rowSep)
}

// rowNameW is what a row spends on the command's own name at pane width w.
func rowNameW(w int) int {
	return min(max(w-rowFixedW()-rowTitleFloor, rowNameMin), rowNameMax)
}

// commandLine is one command under its project's head, title first:
//
//	"    " + name + " " + idx + " " + bell + " · " + payload
//
// The name leads because it is what the user looks a row up by, and it carries
// the row's state in its own color (core.RowNameStyle) rather than next to it —
// a status word spends a column saying what a shade already says, and the
// column it spends is the title's.
//
// The index and bell columns are drawn whether or not the row has anything to
// put in them. Inserts that appear only on the rows that need them shift every
// following column on those rows, so a group of commands lines its titles up
// only by accident; a fixed column lines them up always, which is what makes a
// list of live titles readable down its left edge.
//
// The payload is the title for a live run and the lifecycle word for anything
// else — a run that is over shows where in its life it stands, never its last
// report (D13) — and it is cut to the pane rather than left for the row's own
// truncation, so it ends in an ellipsis instead of mid-word.
func (m Model) commandLine(c core.CommandRow, bg core.RowBg, w int) string {
	nameW := rowNameW(w)
	bell := strings.Repeat(" ", core.GlyphWidth(core.GlyphBell))
	if c.Bell && core.LiveReport(c) {
		bell = core.GlyphBell
	}
	line := bg.Style(core.RowNameStyle(c, m.weak)).
		Render(rowIndent+core.PadCells(core.ClampCells(c.Name, nameW), nameW)) +
		bg.Style(m.weakStyle()).Render(" "+core.ScaleCell(c)) +
		bg.Plain(" "+bell)

	text, live := core.RowPayload(c)
	text = core.ClampCells(text, w-rowFixedW()-nameW)
	if text == "" {
		return line
	}
	style := core.StatusStyle(c.State, c.Pending)
	if live {
		style = core.StyleActive
	}
	return line + bg.Style(core.StyleActive).Render(rowSep) + bg.Style(style).Render(text)
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

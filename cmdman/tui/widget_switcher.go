package tui

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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
	projs []ProjectInfo,
	cmds []CommandInfo,
	cwd string,
	titles map[string]titleStamp,
) []projectGroup {
	cmdGroups := groupFromInfos(cmds)
	claimed := make([]bool, len(cmdGroups))
	byKey := make(map[string]int, len(cmdGroups))
	byName := make(map[string][]int, len(cmdGroups))
	for i, g := range cmdGroups {
		if g.name == "" {
			claimed[i] = true
			continue
		}
		byKey[g.key()] = i
		byName[g.name] = append(byName[g.name], i)
	}

	out := make([]projectGroup, 0, len(projs)+len(cmdGroups))
	for _, p := range projs {
		g := projectGroup{name: p.Name, workdir: p.Workdir}
		if i, ok := matchCommandGroup(p, byKey, byName, claimed); ok {
			claimed[i] = true
			g.commands = cmdGroups[i].commands
			if g.workdir == "" {
				g.workdir = cmdGroups[i].workdir
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
		out[i].active = out[i].workdir != "" && out[i].workdir == cwd
		bucketSort(out[i].commands, titles)
	}
	slices.SortStableFunc(out, func(a, b projectGroup) int {
		if a.active != b.active {
			return boolFirst(a.active)
		}
		return cmp.Compare(a.name, b.name)
	})
	return out
}

// matchCommandGroup finds the command group belonging to a project entry: the
// exact (workdir, name) group, or — for an entry with no workdir — any
// unclaimed group of that name. An entry that carries a workdir never claims a
// group from another directory: same-named projects in different directories
// are different projects.
func matchCommandGroup(
	p ProjectInfo,
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
func bucketSort(cmds []commandRow, titles map[string]titleStamp) {
	slices.SortFunc(cmds, func(a, b commandRow) int {
		return cmp.Or(
			cmp.Compare(titleBucketOf(titles[b.id]), titleBucketOf(titles[a.id])),
			cmp.Compare(a.name, b.name),
			cmp.Compare(a.id, b.id),
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

// aggregateStatus is a project's one status word, from its commands' reported
// statuses only: blocked when any command waits on the user, else working when
// any is working, else idle when any reported done, else "" for a project whose
// commands have said nothing. The unread bell does not compete here — it takes
// over the whole marker slot instead (D22/D23), so this is the dot's meaning
// and the dot's alone. Only live runs are counted: a project must not stay red
// because something it ran yesterday was waiting when it died (D13).
func aggregateStatus(g projectGroup) string {
	status := ""
	for _, c := range g.commands {
		if !liveReport(c) {
			continue
		}
		switch c.status {
		case statusWaiting:
			return statusWaiting
		case statusWorking:
			status = statusWorking
		case statusDone:
			if status == "" {
				status = statusDone
			}
		}
	}
	return status
}

// unreadBell reports whether any of the project's live commands has an unread
// bell; a bell a dead run left behind is as stale as its status.
func unreadBell(g projectGroup) bool {
	return slices.ContainsFunc(g.commands, func(c commandRow) bool {
		return c.bell && liveReport(c)
	})
}

// markerGlyph is the raw glyph in a project's one marker slot, and markerStyle
// its color. The 🔔 replaces the dot while any of the project's commands has an
// unread bell (D23); otherwise the dot is filled when something was reported
// and hollow when nothing was, colored by the aggregate. markerGlyph is what
// gets drawn, so the width math and the drawing cannot pick different glyphs.
func markerGlyph(g projectGroup) string {
	switch {
	case unreadBell(g):
		return glyphBell
	case aggregateStatus(g) == "":
		return glyphHollow
	default:
		return glyphFilled
	}
}

func markerStyle(g projectGroup) lipgloss.Style {
	return reportedStatusStyle(aggregateStatus(g))
}

// renderSwitcher renders the docked column: a grouped list, each project
// heading its group with its one marker slot and its commands listed under it.
// The selected group is highlighted as one solid background block, head line
// and command rows together. The list scrolls inside the pane; the title and
// the hint footer are pinned, so only the group rows move.
//
// A def can dock a switcher into a pane of any height, so the chrome yields to
// the pane rather than overflowing it: the hint line goes first, then the
// title, and the group rows are the last thing standing.
func (m widgetModel) renderSwitcher(w, h int) string {
	h = max(h, 1)
	title, footer := h >= 2, h >= 3
	avail := h
	if title {
		avail--
	}
	if footer {
		avail--
	}

	lines := m.switcherLines(w)
	if len(lines) == 0 {
		lines = []switcherLine{{text: styleActive.Render("No projects.")}}
	}
	off := viewportOffset(lines, m.selected, avail)

	out := make([]string, 0, h)
	if title {
		out = append(out, styleWidgetTitle.Render("projects"))
	}
	out = append(out, linesText(lines[off:min(off+avail, len(lines))])...)
	if footer {
		out = append(out, m.switcherFooter())
	}
	return strings.Join(out, "\n")
}

func linesText(lines []switcherLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.text
	}
	return out
}

// switcherFooter is the pinned last line: the transient error text when there
// is one, else the key hints.
func (m widgetModel) switcherFooter() string {
	if m.status != "" {
		return styleActive.Render(m.status)
	}
	return styleActive.Render("j/k move · q quit")
}

// switcherLines renders the scrollable region: every project's head line
// followed by one line per command, each padded to w so a highlighted group
// forms a solid block.
func (m widgetModel) switcherLines(w int) []switcherLine {
	var lines []switcherLine
	for i, g := range m.groups {
		bg := bgNone
		if i == m.selected {
			bg = bgAccent
		}
		lines = append(lines, switcherLine{text: padLine(m.headLine(g, bg), w, bg), group: i})
		for _, c := range g.commands {
			lines = append(
				lines,
				switcherLine{text: padLine(m.commandLine(c, bg), w, bg), group: i},
			)
		}
	}
	return lines
}

// headLine is a project's head: its marker in a fixed-width slot — so heads
// line up with each other whichever marker shows — then its name. The gap comes
// off the glyph's measured width, not an assumed one, and the margin leads.
func (m widgetModel) headLine(g projectGroup, bg rowBg) string {
	glyph := markerGlyph(g)
	gap := strings.Repeat(" ", max(markerSlot-glyphWidth(glyph), 1))
	name := g.name
	if name == "" {
		name = "(unnamed)"
	}
	head := bg.plain(markerMargin) +
		bg.style(markerStyle(g)).Render(glyph) +
		bg.style(styleWidgetHead).Render(gap+name)
	if g.active {
		// Same word the rest of the TUI marks the cwd project with.
		head += bg.style(styleActive).Render("  active")
	}
	return head
}

// commandLine is one command under its project's head: the command name in the
// weak shade derived from the terminal's own colors, its state badge, an unread
// bell when it has one, and the title it last set — the signal the grouped list
// exists for (D20), fainter still than the name so a group reads as head plus
// detail.
func (m widgetModel) commandLine(c commandRow, bg rowBg) string {
	line := bg.style(m.weakStyle()).Render("    "+padCells(c.name, 12)+" ") +
		rowStateBadge(c, bg)
	if !liveReport(c) {
		// Nothing a finished run said still speaks for it (D13).
		return line
	}
	if c.bell {
		line += bg.plain(" " + glyphBell)
	}
	if c.title != "" {
		line += bg.style(styleActive).Render(" · " + c.title)
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

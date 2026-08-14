package panel

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// statusbarNoProject is the whole left segment when the working directory is
// tied to no project: there is no path in it to shorten, so all of it is fixed.
const statusbarNoProject = " no project"

// statusbarMinPath is the least path the bar keeps when the counts want cells
// from the same row: below it a path is an ellipsis and a syllable, which names
// no place at all, so the counts are what drops instead.
const statusbarMinPath = 12

// renderStatusbar renders the bar left to right as "where you are, then what is
// running, then what this is": the working directory itself with its project's
// marker, the counts across every project, and the version pushed to the right
// edge. Every piece carries the bar's background, so the marker keeps its color
// inside the block instead of being flattened by one outer style.
//
// The three are budgeted as whole segments in that rank rather than laid out
// and clipped: the path shortens for the counts, the counts drop whole when
// even a shortened path cannot sit beside them, and the version — the least of
// the three — is only what is left over. Clipping the row's tail instead cut
// the counts mid-word ("2 runni"), and a cut number reads as a smaller number
// rather than as a cut.
//
// A statusbar entry carves a one-row pane, so this renders exactly one line —
// a second line would scroll the pane.
func (m Model) renderStatusbar(w int) string {
	counts := m.statusbarCounts()
	// An error in the counts' slot is prose, which stays legible cut short, so
	// it keeps the old clip-what-does-not-fit path.
	if m.status == "" && !m.countsFit(w, core.GlyphWidth(counts)) {
		return core.PadLine(m.statusbarLeft(w), w, core.BgAccent)
	}
	left := m.statusbarLeft(w-core.GlyphWidth(counts)) +
		core.BgAccent.Style(core.StyleWidgetBar).Render(counts)
	right := core.BgAccent.Style(core.StyleWidgetBar).Render(m.statusbarVersion() + " ")

	pad := w - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		// Narrow pane: where you are outranks what version says it.
		return core.PadLine(left, w, core.BgAccent)
	}
	return left + core.BgAccent.Plain(strings.Repeat(" ", pad)) + right
}

// countsFit reports whether cw cells of counts can sit next to the place they
// summarize: the path shortens for them — a shortened path still names where
// you are — but only down to what it needs or statusbarMinPath, whichever is
// less.
func (m Model) countsFit(w, cw int) bool {
	fixed, path := m.statusbarPlace()
	return fixed+min(core.GlyphWidth(path), statusbarMinPath)+cw <= w
}

// statusbarPlace is the left segment in its two halves: what it spends before
// the path — the margin, the marker and the space after it, measured off the
// glyph rather than assumed, since the bell is two cells and ●/○ one — and the
// home-abbreviated path itself, not yet cut. One owner for that arithmetic, so
// the budget and the render cannot disagree about it.
func (m Model) statusbarPlace() (fixed int, path string) {
	g, ok := m.activeGroup()
	if !ok {
		return core.GlyphWidth(statusbarNoProject), ""
	}
	fixed = core.GlyphWidth(core.MarkerMargin) + core.GlyphWidth(core.MarkerGlyph(g)) + 1
	return fixed, core.AbbrevHome(g.Workdir, core.HomeDir())
}

// statusbarLeft is the working directory itself, with the marker of the project
// tied to it: the bar names the place rather than the project (D44), since
// several compose projects can run on one directory and the name would then
// misidentify where you are. The path is home-abbreviated and cut to what w
// leaves after the marker, keeping its tail — w is the bar's width with the
// counts already taken out of it.
func (m Model) statusbarLeft(w int) string {
	g, ok := m.activeGroup()
	if !ok {
		return core.BgAccent.Style(core.StyleWidgetBar).Render(statusbarNoProject)
	}
	fixed, path := m.statusbarPlace()
	dir := core.TruncateLeftCells(path, max(w-fixed, 1))
	return core.BgAccent.Plain(core.MarkerMargin) +
		core.BgAccent.Style(core.MarkerStyle(g)).Render(core.MarkerGlyph(g)) +
		core.BgAccent.Style(core.StyleWidgetBar.Bold(true)).Render(" "+dir)
}

// statusbarCounts summarizes every project, not just the active one: how many
// projects there are and how many of their commands are up, plus a failure
// count only when there is one. A load or event-stream error takes the slot
// instead — the bar has one line, and a failure to read the data is more worth
// saying than a count derived from what did load. The segment comes back raw:
// its width is what renderStatusbar budgets the row against, and rendered text
// carries escapes that raw measurement would count as cells.
func (m Model) statusbarCounts() string {
	if m.status != "" {
		return "  " + m.status
	}
	var running, failed int
	for _, g := range m.groups {
		for _, c := range g.Commands {
			switch c.State {
			case model.EventTypeRunning:
				running++
			case model.EventTypeFailed:
				failed++
			}
		}
	}
	counts := fmt.Sprintf("  %d projects · %d running", len(m.groups), running)
	if failed > 0 {
		counts += fmt.Sprintf(" · %d failed", failed)
	}
	return counts
}

func (m Model) statusbarVersion() string {
	if m.version == "" {
		return "cmdman"
	}
	return "cmdman " + m.version
}

// activeGroup returns the project tied to the working directory. The groups are
// sorted active-first, so it is the head of the list when one is active.
func (m Model) activeGroup() (core.ProjectGroup, bool) {
	for _, g := range m.groups {
		if g.Active {
			return g, true
		}
	}
	return core.ProjectGroup{}, false
}

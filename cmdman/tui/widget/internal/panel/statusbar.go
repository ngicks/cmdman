package panel

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// renderStatusbar renders the bar left to right as "where you are, then what is
// running, then what this is": the working directory itself with its project's
// marker, the counts across every project, and the version pushed to the right
// edge. Every piece carries the bar's background, so the marker keeps its color
// inside the block instead of being flattened by one outer style.
//
// A statusbar entry carves a one-row pane, so this renders exactly one line —
// a second line would scroll the pane.
func (m Model) renderStatusbar(w int) string {
	left := m.statusbarLeft(w) + m.statusbarCounts()
	right := core.BgAccent.Style(core.StyleWidgetBar).Render(m.statusbarVersion() + " ")

	pad := w - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		// Narrow pane: where you are outranks what version says it.
		return core.PadLine(left, w, core.BgAccent)
	}
	return left + core.BgAccent.Plain(strings.Repeat(" ", pad)) + right
}

// statusbarLeft is the working directory itself, with the marker of the project
// tied to it: the bar names the place rather than the project (D44), since
// several compose projects can run on one directory and the name would then
// misidentify where you are. The path is home-abbreviated and cut to what is
// left of the bar after the marker, keeping its tail — the rest of the bar is
// what the counts and the version spend, and PadLine takes those back first.
func (m Model) statusbarLeft(w int) string {
	g, ok := m.activeGroup()
	if !ok {
		return core.BgAccent.Style(core.StyleWidgetBar).Render(" no project")
	}
	glyph := core.MarkerGlyph(g)
	// The margin, the marker and the space before the path, measured off the
	// glyph rather than assumed — the bell is two cells and ●/○ one.
	avail := w - core.GlyphWidth(core.MarkerMargin) - core.GlyphWidth(glyph) - 1
	dir := core.ShortPath(g.Workdir, max(avail, 1))
	return core.BgAccent.Plain(core.MarkerMargin) +
		core.BgAccent.Style(core.MarkerStyle(g)).Render(glyph) +
		core.BgAccent.Style(core.StyleWidgetBar.Bold(true)).Render(" "+dir)
}

// statusbarCounts summarizes every project, not just the active one: how many
// projects there are and how many of their commands are up, plus a failure
// count only when there is one. A load or event-stream error takes the slot
// instead — the bar has one line, and a failure to read the data is more worth
// saying than a count derived from what did load.
func (m Model) statusbarCounts() string {
	if m.status != "" {
		return core.BgAccent.Style(core.StyleWidgetBar).Render("  " + m.status)
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
	return core.BgAccent.Style(core.StyleWidgetBar).Render(counts)
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

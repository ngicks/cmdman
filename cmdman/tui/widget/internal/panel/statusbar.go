package panel

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// renderStatusbar renders the bar left to right as "where you are, then what is
// running, then what this is": the working directory's project with its own
// marker, the counts across every project, and the version pushed to the right
// edge. Every piece carries the bar's background, so the marker keeps its color
// inside the block instead of being flattened by one outer style.
//
// A statusbar entry carves a one-row pane, so this renders exactly one line —
// a second line would scroll the pane.
func (m Model) renderStatusbar(w int) string {
	left := m.statusbarLeft() + m.statusbarCounts()
	right := core.BgAccent.Style(core.StyleWidgetBar).Render(m.statusbarVersion() + " ")

	pad := w - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		// Narrow pane: where you are outranks what version says it.
		return core.PadLine(left, w, core.BgAccent)
	}
	return left + core.BgAccent.Plain(strings.Repeat(" ", pad)) + right
}

// statusbarLeft is the project the working directory sits in, with its marker.
func (m Model) statusbarLeft() string {
	g, ok := m.activeGroup()
	if !ok {
		return core.BgAccent.Style(core.StyleWidgetBar).Render(" no project")
	}
	return core.BgAccent.Plain(core.MarkerMargin) +
		core.BgAccent.Style(core.MarkerStyle(g)).Render(core.MarkerGlyph(g)) +
		core.BgAccent.Style(core.StyleWidgetBar.Bold(true)).Render(" "+g.Name)
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

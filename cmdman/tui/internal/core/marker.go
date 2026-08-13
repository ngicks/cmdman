package core

import (
	"slices"

	"charm.land/lipgloss/v2"
)

// AggregateStatus is a project's one status word, from its commands' reported
// statuses only: blocked when any command waits on the user, else working when
// any is working, else idle when any reported done, else "" for a project whose
// commands have said nothing. The unread bell does not compete here — it takes
// over the whole marker slot instead (D22/D23), so this is the dot's meaning
// and the dot's alone. Only live runs are counted: a project must not stay red
// because something it ran yesterday was waiting when it died (D13).
func AggregateStatus(g ProjectGroup) string {
	status := ""
	for _, c := range g.Commands {
		if !LiveReport(c) {
			continue
		}
		switch c.Status {
		case StatusWaiting:
			return StatusWaiting
		case StatusWorking:
			status = StatusWorking
		case StatusDone:
			if status == "" {
				status = StatusDone
			}
		}
	}
	return status
}

// UnreadBell reports whether any of the project's live commands has an unread
// bell; a bell a dead run left behind is as stale as its status.
func UnreadBell(g ProjectGroup) bool {
	return slices.ContainsFunc(g.Commands, func(c CommandRow) bool {
		return c.Bell && LiveReport(c)
	})
}

// MarkerGlyph is the raw glyph in a project's one marker slot, and MarkerStyle
// its color. The 🔔 replaces the dot while any of the project's commands has an
// unread bell (D23); otherwise the dot is filled when something was reported
// and hollow when nothing was, colored by the aggregate. MarkerGlyph is what
// gets drawn, so the width math and the drawing cannot pick different glyphs.
func MarkerGlyph(g ProjectGroup) string {
	switch {
	case UnreadBell(g):
		return GlyphBell
	case AggregateStatus(g) == "":
		return GlyphHollow
	default:
		return GlyphFilled
	}
}

func MarkerStyle(g ProjectGroup) lipgloss.Style {
	return ReportedStatusStyle(AggregateStatus(g))
}

package projectmanager

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// The panel fills its window edge to edge, for the launcher's reason (D27):
// `tmux display-popup` already draws the small centered window and its border,
// and a second box inside it would spend popup space on a doubled frame.
const (
	// managerHeadLines is the project line plus the blank under it, and
	// managerChromeLines everything the two lists do not get: that head, both
	// section titles and the blank between them.
	managerHeadLines   = 2
	managerChromeLines = managerHeadLines + 3
	// managerNameCol caps the service-name column so a long name cannot push the
	// counts off a narrow popup.
	managerNameCol = 24
)

var (
	styleManagerHead = lipgloss.NewStyle().Foreground(core.ColorAccent)
	styleManagerFail = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleManagerWork = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

// glyphCyclable marks a service whose mux leaf is an unpinned cycle target —
// the rows l/h act on.
const glyphCyclable = "↻"

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.viewContent())
	v.AltScreen = m.altScreen
	return v
}

func (m Model) viewContent() string {
	if m.quitting || m.width == 0 || m.height == 0 {
		return ""
	}
	lines := []string{m.headLine(), ""}
	lines = append(lines, m.bodyLines()...)
	foot := m.footerLines()
	for len(lines) < m.height-len(foot) {
		lines = append(lines, "")
	}
	lines = append(lines, foot...)
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	// Nothing may exceed the window: a popup is narrow and a spilling line would
	// wrap, pushing every row below it off its own line.
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(lines, "\n"))
}

// headLine names the project and, on the right, the compose file it came from.
func (m Model) headLine() string {
	head := styleManagerHead.Render("project ") +
		lipgloss.NewStyle().Bold(true).Render(m.projectLabel())
	if tw := m.width - lipgloss.Width(head) - 2; tw >= 10 && m.info.Path != "" {
		path := core.ShortPath(m.info.Path, tw)
		gap := max(m.width-lipgloss.Width(head)-lipgloss.Width(path)-1, 1)
		head += strings.Repeat(" ", gap) + core.StyleActive.Render(path)
	}
	return core.PadLine(head, m.width, core.BgNone)
}

// bodyLines is the two lists, or the reason there are none: a load that never
// answered names its failed probes (D4), and that trail is the body — with no
// project there is nothing else to draw.
func (m Model) bodyLines() []string {
	if m.loadErr != "" {
		return append(
			[]string{styleManagerFail.Render("  ✖ no project")},
			wrapLines("    "+m.loadErr, m.width)...,
		)
	}
	if !m.loaded {
		return []string{core.StyleActive.Render("  loading…")}
	}
	svcBudget, layBudget := splitBudget(m.listBudget(), m.focus,
		len(m.info.Services), len(m.info.Layouts.Names))
	lines := append(
		[]string{m.sectionHead("services", m.focus == zoneServices)},
		m.serviceRows(svcBudget)...,
	)
	lines = append(lines, "", m.sectionHead("layouts", m.focus == zoneLayouts))
	return append(lines, m.layoutRows(layBudget)...)
}

// listBudget is what both lists share: the window minus the chrome and the
// footer.
func (m Model) listBudget() int {
	return max(m.height-managerChromeLines-len(m.footerLines()), 2)
}

// splitBudget hands each list the rows it needs, and when they do not both fit
// gives the focused one everything the other does not need, down to half the
// space — the list being driven is the one worth showing whole.
func splitBudget(total int, focus managerZone, services, layouts int) (svc, lay int) {
	total = max(total, 2)
	if services+layouts <= total {
		return max(services, 1), max(layouts, 1)
	}
	half := max(total/2, 1)
	if focus == zoneServices {
		lay = max(min(layouts, total-half), 1)
		return max(total-lay, 1), lay
	}
	svc = max(min(services, total-half), 1)
	return svc, max(total-svc, 1)
}

func (m Model) sectionHead(name string, focused bool) string {
	st := core.StyleActive
	if focused {
		st = styleManagerHead
	}
	return core.PadLine(st.Render(" "+name), m.width, core.BgNone)
}

// serviceRows draws the services: the cycle-target marker, the name, the
// replica count the +/- keys work from (D11) and the replica the dashboard
// shows.
func (m Model) serviceRows(budget int) []string {
	if len(m.info.Services) == 0 {
		return []string{core.PadLine(
			core.StyleActive.Render("   no services in this project"), m.width, core.BgNone)}
	}
	nameW := 0
	for _, s := range m.info.Services {
		nameW = max(nameW, core.Cells.StringWidth(s.Name))
	}
	nameW = min(nameW, managerNameCol)
	rows := window(budget, len(m.info.Services))
	out := make([]string, 0, budget)
	off := clampOffset(m.svcSel, m.svcOff, rows, len(m.info.Services))
	for i := off; i < min(off+rows, len(m.info.Services)); i++ {
		s := m.info.Services[i]
		bg := managerRowBg(i == m.svcSel, m.focus == zoneServices)
		mark := " "
		if s.Cyclable {
			mark = glyphCyclable
		}
		row := bg.Style(lipgloss.NewStyle().Bold(i == m.svcSel)).Render(
			fmt.Sprintf("  %s %s", mark, core.PadCells(s.Name, nameW))) +
			bg.Plain(fmt.Sprintf("  ×%-3d ", s.Replicas)) +
			bg.Style(core.StyleActive).Render(shownBadge(s))
		out = append(out, core.PadLine(row, m.width, bg))
	}
	return m.appendMore(out, len(m.info.Services)-off-len(out), budget)
}

// shownBadge is which replica the service's dashboard pane displays. A cycle
// target whose windows do not agree — or that no dashboard is showing at all —
// reads as unknown rather than as a position it might not be at (D14).
func shownBadge(s core.ServiceScaleInfo) string {
	switch {
	case s.Shown > 0:
		return fmt.Sprintf("[%d]", s.Shown)
	case s.Cyclable:
		return "[?]"
	default:
		return ""
	}
}

// layoutRows draws the layout list with the running dashboard's own layout
// marked.
func (m Model) layoutRows(budget int) []string {
	names := m.info.Layouts.Names
	if len(names) == 0 {
		return []string{core.PadLine(
			core.StyleActive.Render("   no mux layouts"), m.width, core.BgNone)}
	}
	rows := window(budget, len(names))
	out := make([]string, 0, budget)
	off := clampOffset(m.layoutSel, m.layoutOff, rows, len(names))
	for i := off; i < min(off+rows, len(names)); i++ {
		bg := managerRowBg(i == m.layoutSel, m.focus == zoneLayouts)
		mark := bg.Plain(" ")
		if i == m.info.Layouts.Current {
			mark = bg.Style(core.StyleMarkerIdle).Render(core.GlyphFilled)
		}
		row := bg.Plain("  ") + mark +
			bg.Style(lipgloss.NewStyle().Bold(i == m.layoutSel)).Render(" "+names[i])
		out = append(out, core.PadLine(row, m.width, bg))
	}
	return m.appendMore(out, len(names)-off-len(out), budget)
}

// window is how many rows a list actually draws: a list that does not fit
// spends one of its own lines saying how much it left out. The count comes off
// the budget rather than on top of it — a list a line over its share pushes the
// footer, and the failure line above it, off the bottom of the window.
func window(budget, n int) int {
	if n <= budget || budget <= 1 {
		return min(n, budget)
	}
	return budget - 1
}

// appendMore says what the window left out, in the line window() reserved for
// it — and nowhere else: a budget with no line to spare says nothing.
func (m Model) appendMore(rows []string, hidden, budget int) []string {
	if hidden <= 0 || len(rows) >= budget {
		return rows
	}
	return append(rows, core.PadLine(
		core.StyleActive.Render(fmt.Sprintf("   … %d more", hidden)), m.width, core.BgNone))
}

// clampOffset brings sel into view without moving the window further than it
// has to: a list scrolls under a cursor that walks off its edge.
func clampOffset(sel, off, budget, n int) int {
	if n == 0 || budget <= 0 {
		return 0
	}
	off = min(max(off, 0), max(n-budget, 0))
	if sel < off {
		return sel
	}
	if sel >= off+budget {
		return sel - budget + 1
	}
	return off
}

// clamp keeps both cursors on rows that exist and both windows on their cursor.
// The offsets are stored rather than derived so a resize does not throw away
// where the list was scrolled to.
func (m Model) clamp() Model {
	m.svcSel = min(max(m.svcSel, 0), max(len(m.info.Services)-1, 0))
	m.layoutSel = min(max(m.layoutSel, 0), max(len(m.info.Layouts.Names)-1, 0))
	if m.width == 0 || m.height == 0 {
		return m
	}
	ns, nl := len(m.info.Services), len(m.info.Layouts.Names)
	svcBudget, layBudget := splitBudget(m.listBudget(), m.focus, ns, nl)
	// The same row counts the renderers draw with: a cursor scrolled against the
	// full budget would sit on the line the "… N more" count took.
	m.svcOff = clampOffset(m.svcSel, m.svcOff, window(svcBudget, ns), ns)
	m.layoutOff = clampOffset(m.layoutSel, m.layoutOff, window(layBudget, nl), nl)
	return m
}

// footerLines is the hint line plus, above it, whatever the last key produced:
// a teardown waiting to be confirmed, the action in flight, its failure, or its
// note. Only one of them can be showing — a key clears the previous one before
// it acts — and the question comes first, since the next key answers it.
func (m Model) footerLines() []string {
	hints := core.StyleActive.Render(m.hintText())
	switch {
	case m.pendingDown.Project != "":
		return []string{
			styleManagerWork.Render("· " + core.ComposeDownPrompt(m.pendingDown.Project)),
			hints,
		}
	case m.pending != "":
		return []string{styleManagerWork.Render("· " + m.pending), hints}
	case m.errMsg != "":
		return []string{styleManagerFail.Render("✖ " + m.errMsg), hints}
	case m.loading:
		return []string{styleManagerWork.Render("· reloading…"), hints}
	case m.note != "":
		return []string{styleManagerHead.Render("· " + m.note), hints}
	}
	return []string{hints}
}

func (m Model) hintText() string {
	full := "j/k move · tab → layouts · +/- replicas · l/h shown replica · d/D down · r reload"
	if m.focus == zoneLayouts {
		full = "j/k move · tab → services · enter apply · c cycle · d/D down · r reload"
	}
	if !m.noQuit {
		full += " · q quit"
	}
	if m.width < lipgloss.Width(full) {
		full = "j/k move · tab zone · +/- · l/h · enter · c · d/D · r"
	}
	return full
}

// wrapLines breaks a message onto as many lines as it needs, so a probe trail
// longer than the popup is read rather than cut off at the reason it names
// last. lipgloss breaks on word boundaries and hard-cuts only a word that
// cannot fit at all.
func wrapLines(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(s), "\n")
}

// managerRowBg is the block a row carries: the focused list's cursor is the
// accent one, the other list keeps the weaker block so both say where they
// stand (the launcher's dual cursor, D31).
func managerRowBg(cursor, focused bool) core.RowBg {
	switch {
	case cursor && focused:
		return core.BgAccent
	case cursor:
		return core.BgWeak
	}
	return core.BgNone
}

package launcher

import (
	"fmt"
	"os"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// The launcher fills its window edge to edge (D27): `tmux display-popup` already
// creates the small centered window and draws its border, then hands the program
// that window as its whole terminal. Drawing another box inside it would waste
// popup space and double the borders.
const (
	// launcherHeadLines is the input line plus the blank under it,
	// launcherPaneTop the first row either pane draws on.
	launcherHeadLines = 2
	launcherPaneTop   = launcherHeadLines + 1
	launcherGapLines  = 1
	// launcherLeftPct is the locations pane's share of the width: the left pane
	// carries paths, the right one names.
	launcherLeftPct = 55
)

var (
	styleLauncherPrompt = lipgloss.NewStyle().Foreground(core.ColorAccent)
	styleLauncherWork   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleLauncherFail   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

// launcherSpinnerFrames animates the in-flight marker (D29). This family is one
// cell wide under every runewidth condition; the ◐◓◑◒ half-circles were the other
// candidate but ◐ and ◑ are East-Asian Ambiguous, so two of their four frames
// would measure 2 without the core.Cells pin — a spinner that changes width would
// jitter the whole name column as it turns.
var launcherSpinnerFrames = []string{"◜", "◝", "◞", "◟"}

// launcherMarker is what one row's marker slot shows. bell stays false until the
// monitor serves runtime state (phase 2, D12/D23); the precedence lives here
// already so wiring it up is one assignment rather than a rewrite.
type launcherMarker struct {
	starting bool
	bell     bool
	running  bool
}

// glyph is the raw glyph the marker slot shows. Precedence: a bring-up in flight
// outranks everything, because it is the state the user just asked for and the
// one that is about to change (D29); then an unread bell (D23); then the status
// dot; nothing at all when the entry is neither running nor starting. render
// draws exactly this glyph, so the width math and the drawing cannot pick
// different ones.
func (mk launcherMarker) glyph(frame int) string {
	switch {
	case mk.starting:
		return launcherSpinnerFrames[frame%len(launcherSpinnerFrames)]
	case !mk.running:
		return ""
	case mk.bell:
		return core.GlyphBell
	default:
		// Hollow, not filled: a running project that has reported nothing is
		// "nothing said so far", not a claim that all is idle (D24).
		return core.GlyphHollow
	}
}

// render is the marker slot, styled.
func (mk launcherMarker) render(frame int, bg core.RowBg) string {
	g := mk.glyph(frame)
	switch {
	case g == "":
		return ""
	case mk.starting:
		// Yellow like `working`: something is happening and you did not ask for it
		// to need you.
		return bg.Style(styleLauncherWork).Render(g)
	case g == core.GlyphBell:
		return bg.Plain(g)
	default:
		return bg.Style(core.StyleMarkerIdle).Render(g)
	}
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.viewContent())
	v.AltScreen = m.altScreen
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) viewContent() string {
	if m.quitting || m.width == 0 || m.height == 0 {
		return ""
	}
	out, _, _ := m.screen()
	return out
}

// launcherPane is where a pane's rows landed: the screen line of each visible
// row and the row's index within its list. A click is resolved against the rows
// that were actually drawn rather than against a position computed a second time.
type launcherPane struct {
	x0, x1  int   // screen x range, x1 exclusive
	rowLine []int // screen y of each visible row
	cursor  []int // the row's index within its list
}

func (p launcherPane) at(x, y int) (int, bool) {
	if x < p.x0 || x >= p.x1 {
		return 0, false
	}
	for k, line := range p.rowLine {
		if y == line {
			return p.cursor[k], true
		}
	}
	return 0, false
}

// clickAt moves the left cursor or toggles a project, whichever pane was hit,
// and focuses that pane so the keyboard carries on from where you clicked (D31).
func (m Model) clickAt(x, y int) Model {
	if y == 0 {
		// Clicking the text area puts the keyboard back in it.
		m.focus = zoneInput
		return m
	}
	_, left, right := m.screen()
	if i, ok := left.at(x, y); ok {
		m.focus = zoneLeft
		if i != m.leftSel {
			m.leftSel, m.rightSel, m.rightOff = i, 0, 0
		}
		return m.clampLeft()
	}
	if i, ok := right.at(x, y); ok {
		m.focus, m.rightSel = zoneRight, i
		return m.clampRight().toggle()
	}
	return m
}

// launcherWheelStep is how far one wheel notch scrolls.
const launcherWheelStep = 3

// wheel scrolls the pane the pointer is over, cursor unmoved — scrolling away
// from the cursor is what a wheel is for (D31).
func (m Model) wheel(msg tea.MouseWheelMsg) Model {
	d := launcherWheelStep
	switch msg.Button {
	case tea.MouseWheelUp:
		d = -launcherWheelStep
	case tea.MouseWheelDown:
	default:
		return m
	}
	leftW, _ := m.widths()
	if msg.X < leftW {
		n := len(m.matched())
		m.leftOff = min(max(m.leftOff+d, 0), max(n-1, 0))
		return m
	}
	li, ok := m.currentLoc()
	if !ok {
		return m
	}
	m.rightOff = min(max(m.rightOff+d, 0), max(len(m.locs[li].projects)-1, 0))
	return m
}

func (m Model) budget() int {
	return max(m.height-launcherPaneTop-launcherGapLines-len(m.footerLines()), 1)
}

func (m Model) widths() (leftW, rightW int) {
	leftW = max(m.width*launcherLeftPct/100, 1)
	if m.width >= 24 {
		leftW = min(max(leftW, 12), m.width-12)
	}
	return leftW, max(m.width-leftW-1, 1)
}

// screen renders the whole window edge to edge and reports both panes' row maps.
func (m Model) screen() (out string, left, right launcherPane) {
	leftW, rightW := m.widths()
	f := m.matched()
	scope := "history"
	if m.filter != "" {
		scope = fmt.Sprintf("%d/%d", len(f), len(m.locs))
	}
	lines := []string{m.inputLine(scope), ""}

	leftRows, leftMap := m.leftPane(leftW, f)
	rightRows, rightMap := m.rightPane(rightW)
	lines = append(lines, m.paneHead("locations", leftW, m.focus == zoneLeft)+
		core.StyleActive.Render("│")+m.paneHead(m.rightTitle(), rightW, m.focus == zoneRight))
	for i := range max(len(leftRows), len(rightRows)) {
		l, r := strings.Repeat(" ", leftW), ""
		if i < len(leftRows) {
			l = leftRows[i]
		}
		if i < len(rightRows) {
			r = rightRows[i]
		}
		lines = append(lines, l+core.StyleActive.Render("│")+r)
	}
	foot := m.footerLines()
	for len(lines) < m.height-len(foot) {
		lines = append(lines, "")
	}
	lines = append(lines, foot...)
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	leftMap.x0, leftMap.x1 = 0, leftW
	rightMap.x0, rightMap.x1 = leftW+1, m.width
	// Nothing may exceed the window: a popup is narrow and a spilling line would
	// wrap, pushing every row below it off its own line.
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(lines, "\n")),
		leftMap, rightMap
}

// inputLine is the text area. Its focus has to be legible at a glance, since
// whether a letter types or acts depends on it: focused, the prompt is lit and
// carries a cursor block; unfocused, both go dim and the block is gone.
func (m Model) inputLine(scope string) string {
	if m.focus != zoneInput {
		text := m.filter
		if text == "" {
			text = "(no filter)"
		}
		return core.StyleActive.Render("› " + text + "   " + scope)
	}
	return styleLauncherPrompt.Render("› ") + m.filter + styleLauncherPrompt.Render("▌") +
		core.StyleActive.Render("   "+scope)
}

func (m Model) paneHead(s string, w int, focused bool) string {
	st := core.StyleActive
	if focused {
		st = styleLauncherPrompt
	}
	return core.PadLine(st.Render(" "+s), w, core.BgNone)
}

func (m Model) rightTitle() string {
	li, ok := m.currentLoc()
	if !ok {
		return "projects"
	}
	return "projects at " + m.locs[li].label()
}

// leftPane draws the locations: marker, git-aware label, and as much of the path
// as the column can spare.
func (m Model) leftPane(w int, f []int) ([]string, launcherPane) {
	var (
		out []string
		pm  launcherPane
	)
	cost := make([]int, len(f))
	for i := range cost {
		cost[i] = 1
	}
	n := rowWindow(cost, m.leftOff, m.budget())
	for k := range n {
		idx := m.leftOff + k
		l := m.locs[f[idx]]
		bg := launcherRowBg(idx == m.leftSel, m.focus == zoneLeft)
		mk := l.marker()
		gap := strings.Repeat(" ", max(core.MarkerSlot-core.GlyphWidth(mk.glyph(m.spin)), 0))
		row := bg.Plain(core.MarkerMargin) + mk.render(m.spin, bg) +
			bg.Style(lipgloss.NewStyle().Bold(idx == m.leftSel)).Render(gap+l.label())
		if tw := w - lipgloss.Width(row) - 2; tw >= 10 {
			tail := shortPath(l.Dir, tw)
			gap := max(w-lipgloss.Width(row)-lipgloss.Width(tail)-1, 0)
			row += bg.Plain(strings.Repeat(" ", gap)) +
				bg.Style(core.StyleActive).Render(tail) + bg.Plain(" ")
		}
		pm.rowLine = append(pm.rowLine, launcherPaneTop+len(out))
		pm.cursor = append(pm.cursor, idx)
		out = append(out, core.PadLine(row, w, bg))
	}
	switch {
	case len(f) == 0 && !m.loaded:
		out = append(out, core.PadLine(core.StyleActive.Render("  loading…"), w, core.BgNone))
	case len(f) == 0:
		out = append(
			out,
			core.PadLine(core.StyleActive.Render("  no location matches"), w, core.BgNone),
		)
	}
	if hidden := len(f) - m.leftOff - n; hidden > 0 {
		out = append(
			out,
			core.PadLine(
				core.StyleActive.Render(fmt.Sprintf("  … %d more", hidden)),
				w,
				core.BgNone,
			),
		)
	}
	return out, pm
}

// rightPane draws the projects at the selected location: a toggle box, the
// running marker, the project name and the compose file it comes from.
func (m Model) rightPane(w int) ([]string, launcherPane) {
	var (
		out []string
		pm  launcherPane
	)
	li, ok := m.currentLoc()
	if !ok {
		return out, pm
	}
	ps := m.locs[li].projects
	if len(ps) == 0 {
		return []string{
			core.PadLine(core.StyleActive.Render("  no compose file here"), w, core.BgNone),
		}, pm
	}
	cost := m.rightCost(li)
	n := rowWindow(cost, m.rightOff, m.budget())
	for k := range n {
		idx := m.rightOff + k
		p := ps[idx]
		bg := launcherRowBg(idx == m.rightSel, m.focus == zoneRight)
		box := "[ ]"
		if p.enabled {
			box = "[x]"
		}
		mk := launcherMarker{starting: p.starting, running: p.Running}
		gap := strings.Repeat(" ", max(core.MarkerSlot-core.GlyphWidth(mk.glyph(m.spin)), 0))
		row := bg.Plain(core.MarkerMargin) + bg.Style(m.weakStyle()).Render(box) + bg.Plain(" ") +
			mk.render(m.spin, bg) +
			bg.Style(lipgloss.NewStyle().Bold(idx == m.rightSel)).Render(gap+p.Name)
		file := p.File
		if !p.HasMux {
			file += " · no mux"
		}
		if fw := w - lipgloss.Width(row) - 2; fw >= 6 {
			f := shortPath(file, fw)
			gap := max(w-lipgloss.Width(row)-lipgloss.Width(f)-1, 0)
			row += bg.Plain(strings.Repeat(" ", gap)) +
				bg.Style(core.StyleActive).Render(f) + bg.Plain(" ")
		}
		pm.rowLine = append(pm.rowLine, launcherPaneTop+len(out))
		pm.cursor = append(pm.cursor, idx)
		out = append(out, core.PadLine(row, w, bg))
		if li == m.failedLoc && idx == m.failedPrj {
			// Keep-and-surface (D10): the row stays, the reason it cannot land is
			// spelled out under it, with the removal offer where removal is the
			// answer. core.PadLine truncates from the right, which is what a message
			// wants — shortPath's keep-the-tail is for paths only.
			out = append(
				out,
				core.PadLine(styleLauncherFail.Render("  ✖ "+m.failureText(p)), w, core.BgNone),
			)
		}
	}
	if hidden := len(ps) - m.rightOff - n; hidden > 0 {
		out = append(
			out,
			core.PadLine(
				core.StyleActive.Render(fmt.Sprintf("  … %d more", hidden)),
				w,
				core.BgNone,
			),
		)
	}
	return out, pm
}

// failureText is the inline error line: the reason, plus the removal offer only
// for the entry whose file is gone — ctrl+d drops a history row, and a compose
// file that merely fails to parse is not one to forget.
func (m Model) failureText(p launcherProject) string {
	text := m.failedMsg
	if text == "" {
		text = p.Problem
	}
	if p.Missing {
		text += " · ctrl+d drops it"
	}
	return text
}

func (m Model) footerLines() []string {
	var full string
	switch m.focus {
	case zoneInput:
		full = "type to filter · tab complete · ↑↓ move · enter → locations · esc clear/quit"
	case zoneLeft:
		full = "j/k move · enter → projects · s start · S launch · / input · esc back"
	case zoneRight:
		full = "space toggle · s start enabled · S launch · / input · esc ← locations"
	}
	if m.width < lipgloss.Width(full) {
		full = "enter → · j/k move · s start · S launch · / input · esc back"
	}
	hints := core.StyleActive.Render(full)
	if m.note != "" {
		return []string{styleLauncherPrompt.Render("· " + m.note), hints}
	}
	return []string{hints}
}

// launcherRowBg is the background a row carries. The cursor of the unfocused
// pane keeps the weaker block, so both panes show where they stand without
// competing (D31 kept the dual cursor).
func launcherRowBg(cursor, focused bool) core.RowBg {
	switch {
	case cursor && focused:
		return core.BgAccent
	case cursor:
		return core.BgWeak
	}
	return core.BgNone
}

// homeDir is the prefix shortPath abbreviates to "~".
var homeDir = sync.OnceValue(func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
})

// shortPath abbreviates the home prefix and, when still too long, keeps the tail
// — the end of a path is what distinguishes it.
func shortPath(p string, w int) string {
	return core.TruncateLeftCells(abbrevHome(p, homeDir()), w)
}

// abbrevHome writes the home prefix as "~", and only on a path-component
// boundary: a sibling home like /home/uX shares every letter of /home/u without
// being under it, and "~X/y" would name a directory that does not exist.
func abbrevHome(p, home string) string {
	if home == "" {
		return p
	}
	after, ok := strings.CutPrefix(p, home)
	if !ok || (after != "" && after[0] != '/') {
		return p
	}
	return "~" + after
}

// deriveWeak recomputes the toggle-box shade from whatever the terminal has
// reported so far, so the two answers may arrive in either order (D26).
func (m Model) deriveWeak() Model {
	if weak := core.DeriveWeak(m.termFg, m.termBg, m.fgDark); weak != nil {
		m.weak = weak
	}
	return m
}

func (m Model) weakStyle() lipgloss.Style { return core.WeakStyle(m.weak) }

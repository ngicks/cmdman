package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

var (
	styleTitle     = lipgloss.NewStyle().Bold(true)
	styleTabActive = lipgloss.NewStyle().Bold(true).Reverse(true)
	styleTabIdle   = lipgloss.NewStyle().Faint(true)
	styleActive    = lipgloss.NewStyle().Faint(true)
	styleSelected  = lipgloss.NewStyle().Reverse(true)
	styleFooter    = lipgloss.NewStyle().Faint(true)
	styleVersion   = lipgloss.NewStyle().Faint(true)
	stylePopup     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	stylePopupBtn  = lipgloss.NewStyle().Padding(0, 1)
	stylePopupSel  = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
)

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("cmdman tui"))
	b.WriteByte('\n')
	b.WriteString(m.renderTabBar())
	b.WriteByte('\n')
	b.WriteString(m.renderFilterLine())
	b.WriteByte('\n')

	bodyHeight := max(height-5, 1) // title, tabs, filter, 2 footer lines
	body := m.renderBody(width, bodyHeight)
	b.WriteString(body)
	b.WriteByte('\n')
	b.WriteString(m.renderFooter(width))

	out := b.String()
	if m.helpOpen {
		return overlay(m.renderHelp(), width, height)
	}
	if m.popup.open() {
		return overlay(m.renderPopup(), width, height)
	}
	return out
}

func (m Model) renderTabBar() string {
	names := []string{"Commands", "Compose"}
	parts := make([]string, 0, len(names))
	for i, n := range names {
		if tab(i) == m.active {
			parts = append(parts, styleTabActive.Render(" "+n+" "))
		} else {
			parts = append(parts, styleTabIdle.Render(" "+n+" "))
		}
	}
	return strings.Join(parts, " ")
}

func (m Model) renderFilterLine() string {
	var filter string
	var focused bool
	if m.active == tabCommands {
		filter = m.commands.filter
		focused = m.commands.filtering
	} else {
		filter = m.compose.filter
		focused = m.compose.filtering
	}
	cursor := ""
	if focused {
		cursor = "_"
	}
	return fmt.Sprintf("Filter: %s%s", filter, cursor)
}

func (m Model) renderBody(width, height int) string {
	if m.active == tabCommands {
		return m.renderCommandsBody(width, height)
	}
	return m.renderComposeBody(width, height)
}

func (m Model) renderCommandsBody(width, height int) string {
	leftW := width / 2
	if leftW < 20 {
		leftW = width
	}
	rightW := width - leftW - 1
	left := m.renderCommandList(leftW, height)
	if rightW < 4 {
		return left
	}
	right := m.renderPreview(rightW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func (m Model) renderCommandList(width, height int) string {
	rows := m.commands.visibleRows()
	lines := make([]string, 0, len(rows))
	if len(rows) == 0 {
		lines = append(lines, styleActive.Render("No compose commands."))
	}
	for i, r := range rows {
		var line string
		if r.kind == visProject {
			g := m.commands.groups[r.group]
			glyph := "v"
			if m.commands.folded(r.group) && m.commands.filter == "" {
				glyph = ">"
			}
			line = fmt.Sprintf("%s %s %s", glyph, projectMarker, g.name)
			if g.active {
				line += "   " + styleActive.Render("active")
			}
		} else {
			c := m.commands.groups[r.group].commands[r.cmd]
			prefix := "  "
			if i == m.commands.selected {
				prefix = "> "
			}
			label := displayLabel(c.state, c.exitCode)
			if c.pending != "" {
				label = c.pending + "…"
			}
			line = fmt.Sprintf("  %s%-18s %s", prefix, truncate(c.name, 18), label)
		}
		if i == m.commands.selected {
			line = styleSelected.Render(padRight(line, width))
		} else {
			line = truncate(line, width)
		}
		lines = append(lines, line)
	}
	return clampLines(lines, height, m.commands.selected)
}

func (m Model) renderPreview(width, height int) string {
	p := m.commands.preview
	var lines []string
	switch p.status {
	case previewNoStorage:
		lines = []string{styleActive.Render("No log storage configured for this command.")}
	case previewError:
		lines = []string{
			styleActive.Render("Unable to read command output:"),
			p.errMsg,
		}
	case previewLoading:
		lines = []string{styleActive.Render("Loading…")}
	case previewOK:
		lines = p.lines
	default:
		lines = []string{styleActive.Render("No output yet.")}
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, styleActive.Render("Preview"))
	for _, l := range lines {
		out = append(out, truncate(l, width))
	}
	return clampLines(out, height, 0)
}

func (m Model) renderComposeBody(width, height int) string {
	rows := m.compose.visibleRows()
	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, styleActive.Render("Compose projects"))
	if len(rows) == 0 {
		lines = append(lines, "", styleActive.Render("No compose projects found."))
		return clampLines(lines, height, 0)
	}
	for i, r := range rows {
		prefix := "  "
		if i == m.compose.selected {
			prefix = "> "
		}
		active := "      "
		if r.active {
			active = "active"
		}
		meta := fmt.Sprintf("%d commands", r.commands)
		badge := r.modified
		if r.hasMux {
			badge = "mux"
		}
		line := fmt.Sprintf("%s%s %-16s %s   %-12s   %s",
			prefix, projectMarker, truncate(r.name, 16), active, meta, badge)
		if i == m.compose.selected {
			line = styleSelected.Render(padRight(line, width))
		} else {
			line = truncate(line, width)
		}
		lines = append(lines, line)
	}
	return clampLines(lines, height, m.compose.selected+1)
}

func (m Model) renderFooter(width int) string {
	var hints string
	if m.active == tabCommands {
		hints = "tab next  j/k move  h/l fold  / filter  s start  S stop  r restart  a attach  x remove  ? help  q quit"
	} else {
		hints = "tab next  j/k move  / filter  enter open  c cycle mux  r refresh  ? help  q quit"
	}
	status := m.status
	line1 := styleFooter.Render(truncate(hints, width))
	ver := m.version
	if ver == "" {
		ver = "devel"
	}
	verRender := styleVersion.Render(ver)
	left := truncate(status, width-runewidth.StringWidth(ver)-1)
	pad := max(width-runewidth.StringWidth(left)-runewidth.StringWidth(ver), 1)
	line2 := left + strings.Repeat(" ", pad) + verRender
	return line1 + "\n" + line2
}

// clampLines pads/truncates a slice of lines to exactly height lines, scrolling
// so that the row at focus is visible.
func clampLines(lines []string, height, focus int) string {
	height = max(height, 1)
	start := 0
	if len(lines) > height {
		if focus >= height {
			start = focus - height + 1
		}
		if start+height > len(lines) {
			start = len(lines) - height
		}
		start = max(start, 0)
	}
	end := min(start+height, len(lines))
	view := lines[start:end]
	for len(view) < height {
		view = append(view, "")
	}
	return strings.Join(view, "\n")
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return runewidth.Truncate(s, w, "")
}

func padRight(s string, w int) string {
	cur := runewidth.StringWidth(s)
	if cur >= w {
		return runewidth.Truncate(s, w, "")
	}
	return s + strings.Repeat(" ", w-cur)
}

// overlay centers box content on a cleared frame. It is a simple full-redraw
// overlay: the modal box is drawn vertically and horizontally centered.
// Bubble Tea repaints the whole frame each render so this is enough.
func overlay(box string, width, height int) string {
	boxLines := strings.Split(box, "\n")
	boxH := len(boxLines)
	top := max((height-boxH)/2, 0)
	var b strings.Builder
	for range top {
		b.WriteByte('\n')
	}
	for i, l := range boxLines {
		lw := runewidth.StringWidth(stripANSI(l))
		left := max((width-lw)/2, 0)
		b.WriteString(strings.Repeat(" ", left))
		b.WriteString(l)
		if i < len(boxLines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// stripANSI is a tiny helper to measure rendered width ignoring escape codes.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

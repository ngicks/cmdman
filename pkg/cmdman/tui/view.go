package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// Charm-ish purple palette (256-color indices degrade gracefully).
var (
	colorBorder = lipgloss.Color("63")  // indigo border
	colorAccent = lipgloss.Color("99")  // purple titles/accents
	colorOnAcc  = lipgloss.Color("231") // near-white text on a purple fill
)

var (
	styleTitle     = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorOnAcc).
			Background(colorBorder).
			Padding(0, 1)
	styleTabIdle  = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	styleBoxTitle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleBorder   = lipgloss.NewStyle().Foreground(colorBorder)
	styleActive   = lipgloss.NewStyle().Faint(true)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(colorOnAcc).Background(colorBorder)
	styleFooter   = lipgloss.NewStyle().Faint(true)
	styleVersion  = lipgloss.NewStyle().Foreground(colorAccent)
	stylePopup    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)
	stylePopupBtn = lipgloss.NewStyle().Padding(0, 1)
	stylePopupSel = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorOnAcc).
			Background(colorBorder).
			Padding(0, 1)

	// Status-marker colors mirror the compose TTY reporter so the TUI shows the
	// same indicators compose emits to the terminal.
	styleMarkProgress = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // in progress
	styleMarkPending  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // created (ready)
	styleMarkOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // running/exited
	styleMarkErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // failed
)

// statusGlyph returns the single-cell status marker for a command, matching the
// compose progress reporter: spinner while in progress, ◌ created, ● running,
// ✔ exited, ✘ failed.
func statusGlyph(state model.EventType, pending string, frame int) string {
	if pending != "" || state == model.EventTypeStarting {
		return spinnerFrames[frame%len(spinnerFrames)]
	}
	switch state {
	case model.EventTypeCreated:
		return "◌"
	case model.EventTypeRunning:
		return "●"
	case model.EventTypeExited:
		return "✔"
	case model.EventTypeFailed:
		return "✘"
	default:
		return " "
	}
}

// statusStyle returns the color style paired with statusGlyph.
func statusStyle(state model.EventType, pending string) lipgloss.Style {
	if pending != "" || state == model.EventTypeStarting {
		return styleMarkProgress
	}
	switch state {
	case model.EventTypeCreated:
		return styleMarkPending
	case model.EventTypeRunning, model.EventTypeExited:
		return styleMarkOK
	case model.EventTypeFailed:
		return styleMarkErr
	default:
		return lipgloss.NewStyle()
	}
}

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
	b.WriteByte(' ')
	b.WriteString(styleTitle.Render("cmdman tui"))
	b.WriteByte('\n')
	b.WriteByte(' ')
	b.WriteString(m.renderTabBar())
	b.WriteByte('\n')
	b.WriteString(m.renderFilterBox(width))
	b.WriteByte('\n')

	// title(1) + tabbar(1) + filter box(3) + footer(2) = 7
	bodyHeight := max(height-7, 3)
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
			parts = append(parts, styleTabActive.Render(n))
		} else {
			parts = append(parts, styleTabIdle.Render(n))
		}
	}
	return strings.Join(parts, " ")
}

// renderFilterBox renders the filter input as a bordered "Filter" section.
func (m Model) renderFilterBox(width int) string {
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
	return box("Filter", filter+cursor, width, 3)
}

func (m Model) renderBody(width, height int) string {
	if m.active == tabCommands {
		return m.renderCommandsBody(width, height)
	}
	return m.renderComposeBody(width, height)
}

func (m Model) renderCommandsBody(width, height int) string {
	leftW := width / 2
	rightW := width - leftW
	if rightW < 12 {
		// Too narrow for a side-by-side preview; show the list full width.
		return m.renderCommandList("Commands", width, height)
	}
	left := m.renderCommandList("Commands", leftW, height)
	right := m.renderPreview(rightW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) renderCommandList(title string, width, height int) string {
	cw := max(width-2, 1)
	ch := max(height-2, 1)
	rows := m.commands.visibleRows()
	lines := make([]string, 0, len(rows))
	if len(rows) == 0 {
		lines = append(lines, styleActive.Render("No commands."))
	}
	for i, r := range rows {
		selected := i == m.commands.selected
		var plain, styled string
		if r.kind == visProject {
			g := m.commands.groups[r.group]
			glyph := "v"
			if m.commands.folded(r.group) && m.commands.filter == "" {
				glyph = ">"
			}
			plain = fmt.Sprintf("%s %s %s", glyph, projectMarker, g.name)
			styled = plain
			if g.active {
				plain += "   active"
				styled += "   " + styleActive.Render("active")
			}
		} else {
			c := m.commands.groups[r.group].commands[r.cmd]
			prefix := "  "
			if selected {
				prefix = "> "
			}
			// Commands under a project header are indented beneath it; standalone
			// commands (no project name) sit at the top level with no header.
			indent := "  "
			if m.commands.groups[r.group].name == "" {
				indent = ""
			}
			label := displayLabel(c.state, c.exitCode)
			if c.pending != "" {
				label = c.pending + "…"
			}
			// Status marker (same indicators as compose) to the left of the
			// command name, so a start cascade is visible as it progresses.
			glyph := statusGlyph(c.state, c.pending, m.spinner)
			name := truncate(c.name, 16)
			plain = fmt.Sprintf("%s%s%s %-16s %s", indent, prefix, glyph, name, label)
			styled = fmt.Sprintf("%s%s%s %-16s %s", indent, prefix,
				statusStyle(c.state, c.pending).Render(glyph), name, label)
		}
		if selected {
			// lipgloss Width pads the background to a full-width selection bar.
			lines = append(lines, styleSelected.Width(cw).Render(truncate(plain, cw)))
		} else {
			lines = append(lines, styled)
		}
	}
	content := clampLines(lines, ch, m.commands.selected)
	return box(title, content, width, height)
}

func (m Model) renderPreview(width, height int) string {
	ch := max(height-2, 1)
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
	// box truncates each line ANSI-aware to the inner width.
	content := clampLines(lines, ch, 0)
	return box("Preview", content, width, height)
}

func (m Model) renderComposeBody(width, height int) string {
	cw := max(width-2, 1)
	ch := max(height-2, 1)
	rows := m.compose.visibleRows()
	if len(rows) == 0 {
		content := clampLines([]string{styleActive.Render("No compose projects found.")}, ch, 0)
		return box("Compose projects", content, width, height)
	}
	lines := make([]string, 0, len(rows))
	for i, r := range rows {
		selected := i == m.compose.selected
		prefix := "  "
		if selected {
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
		if selected {
			lines = append(lines, styleSelected.Width(cw).Render(truncate(line, cw)))
		} else {
			lines = append(lines, line)
		}
	}
	content := clampLines(lines, ch, m.compose.selected)
	return box("Compose projects", content, width, height)
}

func (m Model) renderFooter(width int) string {
	var hints string
	if m.active == tabCommands {
		hints = "tab next  j/k move  h/l fold  / filter  s start  S stop  r restart  " +
			"a attach  x remove  ? help  q quit"
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

// box draws content inside a rounded purple border with title embedded in the
// top edge. totalW and totalH are the outer dimensions including the border.
// content is normalized to fit the inner area (totalH-2 lines, each totalW-2
// wide), so callers do not need to pre-size it exactly.
func box(title, content string, totalW, totalH int) string {
	totalW = max(totalW, 2)
	totalH = max(totalH, 2)
	cw := totalW - 2
	ch := totalH - 2

	src := strings.Split(content, "\n")
	out := make([]string, 0, totalH)
	out = append(out, topBorder(title, cw))
	bar := styleBorder.Render("│")
	for i := range ch {
		var l string
		if i < len(src) {
			l = src[i]
		}
		// ANSI-aware: content lines may already carry color codes, so measure
		// and truncate with ansi helpers, not runewidth (which miscounts the
		// escape sequences and corrupts both the content and the right border).
		l = ansi.Truncate(l, cw, "")
		if pad := cw - ansi.StringWidth(l); pad > 0 {
			l += strings.Repeat(" ", pad)
		}
		out = append(out, bar+l+bar)
	}
	out = append(out, bottomBorder(cw))
	return strings.Join(out, "\n")
}

// topBorder renders the top edge of a box, embedding title as "╭─ title ──╮".
func topBorder(title string, cw int) string {
	if title == "" {
		return styleBorder.Render("╭" + strings.Repeat("─", cw) + "╮")
	}
	t := " " + title + " "
	lead := 1
	if runewidth.StringWidth(t)+lead > cw {
		t = runewidth.Truncate(t, max(cw-lead, 0), "")
	}
	tw := runewidth.StringWidth(t)
	rest := max(cw-lead-tw, 0)
	return styleBorder.Render("╭"+strings.Repeat("─", lead)) +
		styleBoxTitle.Render(t) +
		styleBorder.Render(strings.Repeat("─", rest)+"╮")
}

func bottomBorder(cw int) string {
	return styleBorder.Render("╰" + strings.Repeat("─", cw) + "╯")
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

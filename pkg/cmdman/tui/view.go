package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	stylePath     = lipgloss.NewStyle().Faint(true) // dim working-directory paths
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

// View implements tea.Model. In v2 the view carries its own terminal modes
// (alternate screen, etc.), so AltScreen is requested here per-frame rather
// than as a program option.
func (m Model) View() tea.View {
	v := tea.NewView(m.viewContent())
	v.AltScreen = m.altScreen
	return v
}

func (m Model) viewContent() string {
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
	b.WriteString(m.renderTopBar(width))
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
	if m.defViewer.open {
		return overlay(m.renderDefViewer(), width, height)
	}
	if m.composeUp.active {
		return overlay(m.renderComposeUp(), width, height)
	}
	if m.popup.open() {
		return overlay(m.renderPopup(), width, height)
	}
	return out
}

// renderComposeUp renders the live compose-up progress overlay: one per-service
// mark line in first-seen order, mirroring the compose TTY reporter's glyphs.
func (m Model) renderComposeUp() string {
	w, h := m.composeUpSize()
	title := "Compose up"
	if m.composeUp.project != "" {
		title = "Compose up — " + m.composeUp.project
	}
	var lines []string
	if len(m.composeUp.order) == 0 {
		lines = []string{styleActive.Render("Starting…")}
	} else {
		for _, name := range m.composeUp.order {
			mk := m.composeUp.marks[name]
			glyph := composeUpGlyph(mk, m.spinner)
			styled := composeUpStyle(mk).Render(glyph)
			lines = append(lines, fmt.Sprintf("%s %-20s %s", styled, truncate(name, 20), mk.phase))
		}
	}
	content := clampLines(lines, max(h-2, 1), 0)
	return box(title, content, w, h)
}

// composeUpSize returns the outer overlay box dimensions, growing to fit the
// service list but clamped to the screen.
func (m Model) composeUpSize() (w, h int) {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}
	rows := max(len(m.composeUp.order), 1) + 2 // content + top/bottom border
	return max(width-8, 30), min(max(rows, 5), max(height-4, 5))
}

// composeUpGlyph picks the single-cell mark for a service, matching statusGlyph:
// spinner while in flight, ● running, ◌ created, ✘ failed, ✔ otherwise terminal.
func composeUpGlyph(mk composeUpMark, frame int) string {
	if !mk.terminal {
		return spinnerFrames[frame%len(spinnerFrames)]
	}
	if mk.failed {
		return "✘"
	}
	switch mk.phase {
	case "running":
		return "●"
	case "created", "recreated", "unchanged":
		return "◌"
	default:
		return "✔"
	}
}

// composeUpStyle returns the color style paired with composeUpGlyph, mirroring
// statusStyle / the compose TTY reporter colors.
func composeUpStyle(mk composeUpMark) lipgloss.Style {
	if !mk.terminal {
		return styleMarkProgress
	}
	if mk.failed {
		return styleMarkErr
	}
	switch mk.phase {
	case "created", "recreated", "unchanged":
		return styleMarkPending
	default:
		return styleMarkOK
	}
}

// renderDefViewer renders the read-only definition viewer overlay: the project's
// raw compose YAML, scrolled to defViewer.scroll.
func (m Model) renderDefViewer() string {
	w, h := m.defViewerSize()
	title := "Definition"
	if m.defViewer.project != "" {
		title = "Definition — " + m.defViewer.project
	}
	title += "  (j/k scroll  esc close)"
	var lines []string
	switch {
	case m.defViewer.loading:
		lines = []string{styleActive.Render("Loading…")}
	case m.defViewer.errMsg != "":
		lines = []string{styleActive.Render("Unable to read definition:"), m.defViewer.errMsg}
	default:
		lines = m.defViewer.lines
	}
	content := scrollLines(lines, max(h-2, 1), m.defViewer.scroll)
	return box(title, content, w, h)
}

// renderTopBar renders the title line: "cmdman tui" on the left and the current
// working directory on the right. The cwd is the directory used for
// active-project detection, so surfacing it tells the user which directory the
// dashboard is scoped to. The path is dimmed and left-truncated so its leaf (the
// most specific, useful part) stays visible when it does not fit.
func (m Model) renderTopBar(width int) string {
	const title = "cmdman tui"
	left := " " + styleTitle.Render(title)
	leftW := 1 + runewidth.StringWidth(title)
	if m.cwd == "" || width-leftW < 8 {
		return left
	}
	label := "cwd: " + m.cwd
	label = truncateLeft(label, width-leftW-2) // keep a 2-cell gap from the title
	right := stylePath.Render(label)
	pad := max(width-leftW-runewidth.StringWidth(label), 1)
	return left + strings.Repeat(" ", pad) + right
}

func (m Model) renderTabBar() string {
	names := TabNames()
	parts := make([]string, 0, len(names))
	for i, n := range names {
		if Tab(i) == m.active {
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
	switch m.active {
	case TabCommands:
		filter = m.commands.filter
		focused = m.commands.filtering
	case TabCompose:
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
	switch m.active {
	case TabCommands:
		return m.renderCommandsBody(width, height)
	case TabCompose:
		return m.renderComposeBody(width, height)
	default:
		return m.renderLayoutBody(width, height)
	}
}

// renderLayoutBody renders the Layout tab: the current project's mux layouts in
// definition order. The running dashboard's current layout is marked with ●; the
// selection is shown with the selection bar.
func (m Model) renderLayoutBody(width, height int) string {
	cw := max(width-2, 1)
	ch := max(height-2, 1)
	title := "Layouts"
	if m.layout.project != "" {
		title = "Layouts — " + m.layout.project
	}
	rows := m.layout.rows
	if len(rows) == 0 {
		msg := "No mux layouts for the current project."
		if !m.layout.loaded {
			msg = "Loading…"
		}
		content := clampLines([]string{styleActive.Render(msg)}, ch, 0)
		return box(title, content, width, height)
	}
	lines := make([]string, 0, len(rows))
	for i, r := range rows {
		selected := i == m.layout.selected
		prefix := "  "
		if selected {
			prefix = "> "
		}
		marker := " "
		if i == m.layout.current {
			marker = "●"
		}
		plain := fmt.Sprintf("%s%s %s", prefix, marker, r.name)
		switch {
		case selected:
			lines = append(lines, styleSelected.Width(cw).Render(truncate(plain, cw)))
		case i == m.layout.current:
			lines = append(
				lines,
				fmt.Sprintf("%s%s %s", prefix, styleMarkOK.Render(marker), r.name),
			)
		default:
			lines = append(lines, plain)
		}
	}
	content := clampLines(lines, ch, m.layout.selected)
	return box(title, content, width, height)
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
			// Surface the project's working directory so the user can tell where a
			// compose project was created, dimmed to keep the name prominent.
			if g.workdir != "" {
				plain += "   " + g.workdir
				styled += "   " + stylePath.Render(g.workdir)
			}
		} else {
			c := m.commands.groups[r.group].commands[r.cmd]
			prefix := "  "
			if selected {
				prefix = "> "
			}
			// Commands under a project header are indented beneath it; standalone
			// commands (no project name) sit at the top level with no header.
			standalone := m.commands.groups[r.group].name == ""
			indent := "  "
			if standalone {
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
			// Free-floating commands have no project header to carry the workdir, so
			// show it on the row itself (dimmed).
			if standalone && c.workdir != "" {
				plain += "   " + c.workdir
				styled += "   " + stylePath.Render(c.workdir)
			}
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
	// Terminal-view mode: render the live vt emulator frame. The emulator is
	// sized to the command's PTY, so clampLines + box crop its rows to the pane.
	if p.terminal && p.term != nil {
		content := clampLines(m.renderPreviewTerm(), ch, 0)
		return box("Preview", content, width, height)
	}
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
		plain := fmt.Sprintf("%s%s %-16s %s   %-12s   %s",
			prefix, projectMarker, truncate(r.name, 16), active, meta, badge)
		styled := plain
		// Surface each project's working directory (dimmed) so projects sharing a
		// name across directories are distinguishable at a glance.
		if r.workdir != "" {
			plain += "   " + r.workdir
			styled += "   " + stylePath.Render(r.workdir)
		}
		if selected {
			lines = append(lines, styleSelected.Width(cw).Render(truncate(plain, cw)))
		} else {
			lines = append(lines, styled)
		}
	}
	content := clampLines(lines, ch, m.compose.selected)
	return box("Compose projects", content, width, height)
}

func (m Model) renderFooter(width int) string {
	var hints string
	switch m.active {
	case TabCommands:
		hints = "tab next  j/k move  h/l fold  / filter  s start  S stop  r restart  " +
			"a attach  x remove  ? help  q quit"
	case TabCompose:
		hints = "tab next  j/k move  / filter  enter def  e edit  a up  " +
			"c cycle mux  r refresh  ? help  q quit"
	default:
		hints = "tab next  j/k move  enter apply  r refresh  ? help  q quit"
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

// scrollLines renders lines into a viewport of the given height starting at the
// top line index, clamping top to the content and padding the remainder so the
// viewport always has exactly height lines.
func scrollLines(lines []string, height, top int) string {
	height = max(height, 1)
	top = min(max(top, 0), max(len(lines)-height, 0))
	end := min(top+height, len(lines))
	view := lines[top:end]
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

// truncateLeft truncates s to w cells keeping the tail, prefixing "…" when it
// dropped leading cells. It is used for filesystem paths, where the leaf is the
// most useful part to keep when the whole path does not fit.
func truncateLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	target := w - 1 // reserve one cell for the leading ellipsis
	r := []rune(s)
	width := 0
	i := len(r)
	for i > 0 {
		cw := runewidth.RuneWidth(r[i-1])
		if width+cw > target {
			break
		}
		width += cw
		i--
	}
	return "…" + string(r[i:])
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

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	ansi "github.com/ngicks/cmdman/internal/third_party/charmbracelet-x-ansi"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

var (
	styleTitle     = lipgloss.NewStyle().Bold(true).Foreground(core.ColorAccent)
	styleTabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(core.ColorOnAcc).
			Background(core.ColorBorder).
			Padding(0, 1)
	styleTabIdle  = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	styleBoxTitle = lipgloss.NewStyle().Bold(true).Foreground(core.ColorAccent)
	styleBorder   = lipgloss.NewStyle().Foreground(core.ColorBorder)
	styleSelected = lipgloss.NewStyle().
			Bold(true).
			Foreground(core.ColorOnAcc).
			Background(core.ColorBorder)
	styleFooter  = lipgloss.NewStyle().Faint(true)
	styleVersion = lipgloss.NewStyle().Foreground(core.ColorAccent)
	stylePath    = lipgloss.NewStyle().Faint(true) // dim working-directory paths
	stylePopup   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(core.ColorBorder).
			Padding(0, 1)
	stylePopupBtn = lipgloss.NewStyle().Padding(0, 1)
	stylePopupSel = lipgloss.NewStyle().
			Bold(true).
			Foreground(core.ColorOnAcc).
			Background(core.ColorBorder).
			Padding(0, 1)

	// The status-marker colors live in core so core.StatusStyle — which the
	// switcher rows share with this package's lists — can reach them; they are
	// bound here because this file's own renderers name them directly.
	styleMarkProgress = core.StyleMarkProgress
	styleMarkPending  = core.StyleMarkPending
	styleMarkOK       = core.StyleMarkOK
	styleMarkErr      = core.StyleMarkErr
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
		lines = []string{core.StyleActive.Render("Starting…")}
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
// core.StatusStyle / the compose TTY reporter colors.
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
		lines = []string{core.StyleActive.Render("Loading…")}
	case m.defViewer.errMsg != "":
		lines = []string{core.StyleActive.Render("Unable to read definition:"), m.defViewer.errMsg}
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
	default:
		return m.renderComposeBody(width, height)
	}
}

func (m Model) renderCommandsBody(width, height int) string {
	leftW := width / 2
	rightW := width - leftW
	if rightW < 12 {
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
		lines = append(lines, core.StyleActive.Render("No commands."))
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
			// The project's one attention marker (D14/D23), the same one the
			// switcher shows, between the fold arrow and the name: whichever
			// surface the user is looking at says the same thing about the
			// project.
			mark := core.MarkerGlyph(g)
			// The marker sits in the same fixed-width slot the switcher gives it,
			// measured off the glyph rather than assumed: 🔔 is two cells and ●/○
			// one, so a single space after it would move the name a column between
			// rows.
			gap := strings.Repeat(" ", max(core.MarkerSlot-core.GlyphWidth(mark), 1))
			plain = fmt.Sprintf("%s %s %s%s", glyph, projectMarker, mark, gap+g.Name)
			styled = fmt.Sprintf("%s %s %s%s", glyph, projectMarker,
				core.MarkerStyle(g).Render(mark), gap+g.Name)
			if g.Active {
				plain += "   active"
				styled += "   " + core.StyleActive.Render("active")
			}
			// Surface the project's working directory so the user can tell where a
			// compose project was created, dimmed to keep the name prominent.
			if g.Workdir != "" {
				plain += "   " + g.Workdir
				styled += "   " + stylePath.Render(g.Workdir)
			}
		} else {
			c := m.commands.groups[r.group].Commands[r.cmd]
			prefix := "  "
			if selected {
				prefix = "> "
			}
			// Commands under a project header are indented beneath it; standalone
			// commands (no project name) sit at the top level with no header.
			standalone := m.commands.groups[r.group].Name == ""
			indent := "  "
			if standalone {
				indent = ""
			}
			label := core.DisplayLabel(c.State, c.ExitCode)
			if c.Pending != "" {
				label = c.Pending + "…"
			}
			// Status marker (same indicators as compose) to the left of the
			// command name, so a start cascade is visible as it progresses.
			glyph := statusGlyph(c.State, c.Pending, m.spinner)
			name := truncate(c.Name, 16)
			plain = fmt.Sprintf("%s%s%s %-16s %s", indent, prefix, glyph, name, label)
			styled = fmt.Sprintf("%s%s%s %-16s %s", indent, prefix,
				core.StatusStyle(c.State, c.Pending).Render(glyph), name, label)
			// Which replica this is when it is one of several (D44), next to the
			// state and before anything the command said: it is the row's
			// identity, so it stays on a row D13 has taken the words off. Dimmed
			// like the paths, so a title after it still reads as the command's.
			if badge := core.ScaleBadge(c); badge != "" {
				plain += badge
				styled += stylePath.Render(badge)
			}
			// What the command says about itself, after what the store knows
			// about it: an unread bell (D23), the status it reported with its
			// detail (D12), and the title it set — dimmed like the paths, since
			// it is the command's own words, not the TUI's. Only a live run gets
			// to speak: a run that is over shows its exit state and nothing it
			// said before it ended (D13), the same rule the switcher rows follow.
			if core.LiveReport(c) {
				if c.Bell {
					plain += "  " + core.GlyphBell
					styled += "  " + core.GlyphBell
				}
				if reported := reportedText(c); reported != "" {
					plain += "  " + reported
					styled += "  " + core.ReportedStatusStyle(c.Status).Render(reported)
				}
				if c.Title != "" {
					plain += "  " + c.Title
					styled += "  " + stylePath.Render(c.Title)
				}
			}
			// Free-floating commands have no project header to carry the workdir, so
			// show it on the row itself (dimmed).
			if standalone && c.Workdir != "" {
				plain += "   " + c.Workdir
				styled += "   " + stylePath.Render(c.Workdir)
			}
		}
		if selected {
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
		lines = []string{core.StyleActive.Render("No log storage configured for this command.")}
	case previewError:
		lines = []string{
			core.StyleActive.Render("Unable to read command output:"),
			p.errMsg,
		}
	case previewLoading:
		lines = []string{core.StyleActive.Render("Loading…")}
	case previewOK:
		lines = p.lines
	default:
		lines = []string{core.StyleActive.Render("No output yet.")}
	}
	content := clampLines(lines, ch, 0)
	return box("Preview", content, width, height)
}

func (m Model) renderComposeBody(width, height int) string {
	cw := max(width-2, 1)
	ch := max(height-2, 1)
	rows := m.compose.visibleRows()
	if len(rows) == 0 {
		content := clampLines(
			[]string{core.StyleActive.Render("No compose projects found.")},
			ch,
			0,
		)
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
	default:
		hints = "tab next  j/k move  / filter  enter def  e edit  a up  " +
			"c cycle mux  r refresh  ? help  q quit"
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
		lw := runewidth.StringWidth(core.StripANSI(l))
		left := max((width-lw)/2, 0)
		b.WriteString(strings.Repeat(" ", left))
		b.WriteString(l)
		if i < len(boxLines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

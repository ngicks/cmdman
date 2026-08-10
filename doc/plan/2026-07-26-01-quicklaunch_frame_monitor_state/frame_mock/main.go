// Command frame_mock is a disposable usability mock for the frame feature
// (doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state). It simulates a
// framed terminal window with fake project data so the frame spec's feel —
// sequential carving, def cycling, shown/hidden switching, the grouped
// bucket-sorted switcher (D20), badges, selection — can be reviewed before
// anything real is built. The side-bar switcher lists every command's title
// under its project group; the shallow bottom-row form shows one aggregated
// badge per project. Each project carries one marker slot: a 🔔 that replaces
// the status dot until the bell is checked (D23), the dot — which reflects
// reported status only (D22), filled green for idle and hollow green for a
// command that never reported (D24) — otherwise. The grouped column list
// scrolls when it outgrows its pane (D25). The selected project's group is a
// solid highlighted block and the focused one a dimmer block (D24). Group
// heads read bright while their app rows take a weak shade derived from the
// terminal's own letter color, queried at startup (D26). Not production code;
// graduates only as reference for the real switcher/statusbar widgets.
//
// Run: go run ./doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state/frame_mock
//
// Keys: f show/hide frame · c cycle defs · j/k move selection ·
// enter switch project (resolves its bells) · b ring a demo bell on the
// selected project (resolves instantly when it is the focused one) ·
// s report demo status · q quit
//
// Mouse: left-click a switcher entry to select and switch to that project,
// exactly as Enter would (D24); the wheel scrolls the column list (D25).
package main

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

func main() {
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// --- fake frame defs (in-code mirror of <config-dir>/frame/<name>.yaml) ---

type entry struct {
	edge      string // top | bottom | left | right
	cells     int    // fixed size in cells; 0 when pct is set
	pct       int    // percent of the remaining rectangle; 0 when cells is set
	component string // switcher | statusbar | (raw command placeholder)
}

type frameDef struct {
	name    string
	entries []entry
}

var defs = []frameDef{
	{name: "side-switcher", entries: []entry{
		{edge: "left", cells: 42, component: "switcher"},
	}},
	{name: "side+statusbar", entries: []entry{
		{edge: "left", pct: 20, component: "switcher"},
		{edge: "bottom", cells: 1, component: "statusbar"},
	}},
	// Same two entries, opposite order: demonstrates "order is the nesting" —
	// here the statusbar spans the full width and the column is shorter.
	{name: "statusbar-first", entries: []entry{
		{edge: "bottom", cells: 1, component: "statusbar"},
		{edge: "left", pct: 20, component: "switcher"},
	}},
	{name: "bottom-launcher", entries: []entry{
		{edge: "bottom", cells: 4, component: "switcher"},
	}},
}

// --- fake project data ---

type command struct {
	name      string
	status    string // working | waiting | done | "" (never reported)
	bell      bool
	title     string
	updatedAt int // fake title-update timestamp (seconds); drives D20's bucket sort
}

type project struct {
	name     string
	commands []command
}

func fakeProjects() []project {
	return []project{
		// claude and devserver land in the same 5 s bucket on purpose: they
		// order by name, not by their racing update times (D20).
		{name: "cmdman (main)", commands: []command{
			{name: "devserver", status: "working", title: "serving :8080", updatedAt: 103},
			{
				name:      "claude",
				status:    "waiting",
				bell:      true,
				title:     "review mon_run.go",
				updatedAt: 101,
			},
		}},
		{name: "webapp (feat/auth)", commands: []command{
			{name: "codex", status: "working", title: "refactor auth", updatedAt: 95},
			{name: "vite", status: ""},
		}},
		{name: "infra (main)", commands: []command{
			{name: "claude", status: "done", title: "tf plan clean", updatedAt: 60},
		}},
		{name: "~/blog", commands: []command{
			{name: "hugo", status: "", bell: true, title: "draft saved", updatedAt: 30},
		}},
		// The first four are the long-running demo states earlier rounds were
		// reviewed against; the rest exist to overflow the pane so the column
		// actually scrolls (D25) at ordinary terminal heights.
		{name: "api (feat/billing)", commands: []command{
			{
				name:      "claude",
				status:    "waiting",
				bell:      true,
				title:     "confirm migration",
				updatedAt: 88,
			},
			{name: "server", status: "working", title: "watching pkg/api", updatedAt: 86},
			{name: "psql", status: "", updatedAt: 12},
		}},
		{name: "mobile (main)", commands: []command{
			{name: "metro", status: "working", title: "bundling ios", updatedAt: 72},
			{name: "simulator", status: "done", title: "booted", updatedAt: 40},
		}},
		{name: "docs (gh-pages)", commands: []command{
			{name: "mkdocs", status: "working", title: "serving :8000", updatedAt: 68},
		}},
		{name: "scratch", commands: []command{
			{name: "repl", status: ""},
		}},
		{name: "etl (main)", commands: []command{
			{name: "airflow", status: "waiting", title: "backfill approval", updatedAt: 55},
			{name: "dbt", status: "done", title: "42 models ok", updatedAt: 50},
		}},
		{name: "~/notes", commands: []command{
			{name: "obsidian", status: "", bell: true, title: "sync conflict", updatedAt: 25},
		}},
		{name: "proxy (main)", commands: []command{
			{name: "caddy", status: "working", title: "reloaded", updatedAt: 20},
			{name: "certbot", status: "done", title: "renewed", updatedAt: 18},
		}},
		{name: "playground", commands: []command{
			{name: "vitest", status: "done", title: "18 passed", updatedAt: 8},
			{name: "tsc", status: "", updatedAt: 5},
		}},
	}
}

// bucketSeconds chunks title-update times so frequently-retitling agents do
// not race each other into list churn (D20; interval tunable).
const bucketSeconds = 5

// bucketSorted orders commands newest-bucket-first, then by name inside a
// bucket.
func bucketSorted(cs []command) []command {
	out := slices.Clone(cs)
	slices.SortFunc(out, func(a, b command) int {
		if d := b.updatedAt/bucketSeconds - a.updatedAt/bucketSeconds; d != 0 {
			return d
		}
		return strings.Compare(a.name, b.name)
	})
	return out
}

// aggregate maps a project's commands onto its dot state from *reported
// status only* (D22): a waiting command means blocked on the user (red);
// otherwise any working command means working (yellow); otherwise idle or —
// when nothing reported at all — unknown, which dot paints the same green
// (D24 amended) — filled for idle, hollow for unknown, so the two facts stay
// distinct on screen as well as here. D21's traffic-light palette stands;
// D14's "bell outranks all" no longer governs this value — an unread bell
// instead takes over the whole marker slot and hides the dot (D23).
func aggregate(p project) string {
	state := "unknown"
	for _, c := range p.commands {
		switch c.status {
		case "waiting":
			return "blocked"
		case "working":
			state = "working"
		case "done":
			if state == "unknown" {
				state = "idle"
			}
		}
	}
	return state
}

// unread reports whether any of the project's commands has an unread bell,
// which makes the project render 🔔 in place of its status dot (D23).
func unread(p project) bool {
	return slices.ContainsFunc(p.commands, func(c command) bool { return c.bell })
}

// --- model ---

type model struct {
	width, height int
	shown         bool
	defIdx        int
	selected      int // switcher cursor
	focused       int // project shown in the main region
	scroll        int // first group line shown in the column form (D25)
	projects      []project

	// The terminal's own palette (D26), nil until it answers the startup
	// query — and some terminals never do.
	termFg, termBg color.Color
	fgDark         bool
	weak           color.Color
}

func newModel() model {
	m := model{shown: true, projects: fakeProjects()}
	// A bell on the already-focused project resolves immediately (D22), so the
	// seeded ones never survive to the first frame.
	for i := range m.projects[m.focused].commands {
		m.projects[m.focused].commands[i].bell = false
	}
	return m
}

func (m model) Init() tea.Cmd {
	// Ask the terminal for its own letter and background colors (D26).
	// Rendering never waits on the reply: the app rows stay faint until it
	// lands, and stay faint forever on terminals that ignore the query.
	return tea.Batch(tea.RequestForegroundColor, tea.RequestBackgroundColor)
}

// weakRatio is how far the app rows travel from the letter color toward the
// background. Much past this they stop being readable on low-contrast themes.
const weakRatio = 0.55

// deriveWeak recomputes the app-row shade from whatever the terminal has
// reported so far, so the two answers may arrive in either order.
func (m model) deriveWeak() model {
	if m.termFg == nil {
		return m
	}
	bg := m.termBg
	if bg == nil {
		// Background unanswered: assume the end opposite the letters, so the
		// blend still moves away from them rather than into them.
		bg = color.Black
		if m.fgDark {
			bg = color.White
		}
	}
	m.weak = blend(m.termFg, bg, weakRatio)
	return m
}

// weakStyle is the app rows' foreground: the terminal's own letter color
// pulled toward its background (D26), so a group reads as bright head plus
// subdued detail on light and dark themes alike. Faint is the fallback for
// terminals that never answer — it is what these rows looked like before.
func (m model) weakStyle() lipgloss.Style {
	if m.weak == nil {
		return styleDim
	}
	return lipgloss.NewStyle().Foreground(m.weak)
}

// blend mixes a toward b along a straight line in RGB. RGBA reports 16-bit
// alpha-premultiplied channels and every color here is opaque, so scaling to
// 8 bits is both correct and enough to keep this dependency-free.
func blend(a, b color.Color, t float64) color.RGBA {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		return uint8(min(math.Round((float64(x)*(1-t)+float64(y)*t)/257), 255))
	}
	return color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 0xff}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.ForegroundColorMsg:
		m.termFg, m.fgDark = msg.Color, msg.IsDark()
		return m.deriveWeak(), nil
	case tea.BackgroundColorMsg:
		m.termBg = msg.Color
		return m.deriveWeak(), nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "f":
			m.shown = !m.shown
		case "c", "tab":
			m.defIdx = (m.defIdx + 1) % len(defs)
			// A new def is a new pane height, so the cursor may now be off-screen.
			m = m.scrollToSelected()
		case "j", "down":
			m.selected = (m.selected + 1) % len(m.projects)
			m = m.scrollToSelected()
		case "k", "up":
			m.selected = (m.selected + len(m.projects) - 1) % len(m.projects)
			m = m.scrollToSelected()
		case "enter":
			m = m.choose()
		case "b":
			// Demo an incoming bell on the project under the cursor. On the
			// focused project it resolves immediately — you are already
			// looking at it, so nothing is set (D22).
			if m.selected != m.focused {
				m.projects[m.selected].commands[0].bell = true
			}
		case "s":
			// Demo a status report cycling on the focused project's first command.
			cyc := map[string]string{
				"":        "working",
				"working": "waiting",
				"waiting": "done",
				"done":    "",
			}
			c := &m.projects[m.focused].commands[0]
			c.status = cyc[c.status]
		}
	case tea.MouseWheelMsg:
		// The wheel scrolls the column list without moving the cursor (D25).
		switch msg.Button {
		case tea.MouseWheelUp:
			m = m.scrollBy(-wheelStep)
		case tea.MouseWheelDown:
			m = m.scrollBy(wheelStep)
		}
	case tea.MouseClickMsg:
		// Clicking a switcher entry is Enter on that entry (D24).
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		if i, ok := m.projectAt(msg.X, msg.Y); ok {
			m.selected = i
			m = m.choose()
		}
	}
	return m, nil
}

// choose is what Enter and a switcher click both do: focus the project under
// the cursor and resolve its bells — selecting through the selector is what
// resolves them (D22).
func (m model) choose() model {
	m.focused = m.selected
	for i := range m.projects[m.focused].commands {
		m.projects[m.focused].commands[i].bell = false
	}
	return m
}

// rect is a carved pane in screen coordinates.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// switcherRect replays carve's arithmetic to find where the switcher landed,
// since the render path joins strings through lipgloss and never reports its
// own geometry. row reports the shallow bottom/top form.
func (m model) switcherRect() (r rect, row, ok bool) {
	r = rect{0, 0, m.width, m.height}
	for _, e := range defs[m.defIdx].entries {
		horizontal := e.edge == "left" || e.edge == "right"
		size := e.cells
		if e.pct > 0 {
			if horizontal {
				size = r.w * e.pct / 100
			} else {
				size = r.h * e.pct / 100
			}
		}
		switch {
		case horizontal:
			size = min(size, r.w-1)
			if e.component == "switcher" {
				if e.edge == "left" {
					return rect{r.x, r.y, size, r.h}, false, true
				}
				return rect{r.x + r.w - size, r.y, size, r.h}, false, true
			}
			if e.edge == "left" {
				r.x += size
			}
			r.w -= size
		default:
			size = min(size, r.h-1)
			if e.component == "switcher" {
				if e.edge == "top" {
					return rect{r.x, r.y, r.w, size}, true, true
				}
				return rect{r.x, r.y + r.h - size, r.w, size}, true, true
			}
			if e.edge == "top" {
				r.y += size
			}
			r.h -= size
		}
	}
	return rect{}, false, false
}

// projectAt maps a screen cell to the project whose switcher entry occupies
// it. Only the shipped defs are handled — one switcher, docked to an edge,
// its body placed at the pane's top-left — which is all the mock renders.
func (m model) projectAt(x, y int) (int, bool) {
	if !m.shown {
		return 0, false
	}
	r, row, ok := m.switcherRect()
	if !ok || !r.contains(x, y) {
		return 0, false
	}
	if row {
		return m.projectAtRow(x-r.x, y-r.y)
	}
	return m.projectAtColumn(y-r.y, r.h)
}

// projectAtColumn maps a pane-local line to its group, through the same
// layout the renderer uses — so a click lands where the row was drawn however
// far the list is scrolled (D25). Any line of a group's block counts.
func (m model) projectAtColumn(dy, h int) (int, bool) {
	l := m.columnLayout(h)
	row := dy - l.top
	if row < 0 || row >= l.avail {
		return 0, false
	}
	line := l.off + row
	for i := range m.projects {
		start, end := m.groupSpan(i)
		if line >= start && line < end {
			return i, true
		}
	}
	return 0, false
}

// groupSpan is the half-open line range project i occupies in the group list:
// its head line plus one line per command.
func (m model) groupSpan(i int) (start, end int) {
	for j := range i {
		start += 1 + len(m.projects[j].commands)
	}
	return start, start + 1 + len(m.projects[i].commands)
}

func (m model) groupLineCount() int {
	n := 0
	for _, p := range m.projects {
		n += 1 + len(p.commands)
	}
	return n
}

// columnLayout places the scrollable group list inside a pane h cells tall.
// The renderer and the hit-test both read it, so a click cannot disagree with
// what was drawn. The two "more" rows are reserved for as long as the list
// scrolls at all, whether or not either has anything to say — a viewport that
// changed height as you reached an end would shift every row under the cursor.
type columnLayout struct {
	top       int // first pane row of the list
	avail     int // rows of the list on screen
	off       int // first list line shown
	scrolling bool
}

func (m model) columnLayout(h int) columnLayout {
	total := m.groupLineCount()
	// header + blank + legend + hints, none of which scroll
	footer := 1 + strings.Count(m.renderLegend(), "\n") + 1
	avail := max(h-1-footer, 1)
	if total <= avail {
		return columnLayout{top: 1, avail: total, off: 0}
	}
	avail = max(avail-2, 1)
	off := min(max(m.scroll, 0), total-avail)
	return columnLayout{top: 2, avail: avail, off: off, scrolling: true}
}

// scrollToSelected brings the cursor's group fully into view. The wheel
// deliberately does not come through here: scrolling away from the cursor is
// what a wheel is for (D25).
func (m model) scrollToSelected() model {
	r, row, ok := m.switcherRect()
	if !ok || row {
		return m
	}
	l := m.columnLayout(r.h)
	start, end := m.groupSpan(m.selected)
	switch {
	case start < l.off:
		m.scroll = start
	case end > l.off+l.avail:
		m.scroll = end - l.avail
	default:
		m.scroll = l.off
	}
	return m
}

// wheelStep is how far one wheel notch moves the list.
const wheelStep = 3

func (m model) scrollBy(d int) model {
	r, row, ok := m.switcherRect()
	if !ok || row {
		return m
	}
	l := m.columnLayout(r.h)
	if !l.scrolling {
		m.scroll = 0
		return m
	}
	m.scroll = min(max(l.off+d, 0), m.groupLineCount()-l.avail)
	return m
}

// projectAtRow maps a pane-local column on the items line to its item, by
// measuring the very strings rowItem builds. dy must be 0: the second line is
// the key hint, not a target.
func (m model) projectAtRow(dx, dy int) (int, bool) {
	if dy != 0 {
		return 0, false
	}
	x := lipgloss.Width(styleFrame.Render("⏵ "))
	sep := lipgloss.Width(styleDim.Render("│"))
	for i := range m.projects {
		w := lipgloss.Width(m.rowItem(i))
		if dx >= x && dx < x+w {
			return i, true
		}
		x += w + sep
	}
	return 0, false
}

// --- view ---

var (
	colorBorder  = lipgloss.Color("63")
	colorAccent  = lipgloss.Color("99")
	colorSelBg   = colorBorder
	colorFocusBg = lipgloss.Color("237")

	styleFrame = lipgloss.NewStyle().Foreground(colorAccent)
	styleDim   = lipgloss.NewStyle().Faint(true)
	// D21 palette, shared by dots and status words: red blocked/waiting,
	// yellow working, green idle/done.
	styleBlocked = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleWorking = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
)

// rowBg is the background a switcher entry carries. Selection is the cursor
// and wins; focus is context and gets the weaker block (D24) — the `*` suffix
// it replaced was redundant beside color and invisible on top of it.
type rowBg int

const (
	bgNone rowBg = iota
	bgFocused
	bgSelected
)

func bgFor(sel, focused bool) rowBg {
	switch {
	case sel:
		return bgSelected
	case focused:
		return bgFocused
	}
	return bgNone
}

// style re-derives st with the row's background; wrapping already-styled text
// in one outer style would let inner color resets punch holes in the
// highlighted block, so every piece of a row carries the bg itself.
func (b rowBg) style(st lipgloss.Style) lipgloss.Style {
	switch b {
	case bgSelected:
		return st.Background(colorSelBg)
	case bgFocused:
		return st.Background(colorFocusBg)
	}
	return st
}

// plain renders s carrying only the row background.
func (b rowBg) plain(s string) string {
	return b.style(lipgloss.NewStyle()).Render(s)
}

// The marker glyphs. They are named because their widths feed the layout, and
// those widths are measured rather than assumed — see glyphWidth.
const (
	glyphBell   = "🔔"
	glyphFilled = "●"
	glyphHollow = "○"
)

// cells is go-runewidth — the table the real TUI lays out with
// (pkg/cmdman/tui/view.go) — pinned to treat East-Asian *Ambiguous* glyphs as
// one cell. That pin is load-bearing, not tidiness: ● and ○ are Ambiguous, so
// under a CJK locale the package default calls them 2 while lipgloss (uniseg),
// which actually draws them, still renders 1. Measuring the gap with one ruler
// and padding the row with the other tears the name column apart — measured at
// [3 3 3 4 4 3] instead of [4 4 4 4 4 4]. An explicit Condition also keeps this
// independent of the ambient locale and of package-global mutation.
var cells = &runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}

// glyphWidth measures a raw glyph. lipgloss.Width stays for strings that have
// already been rendered: those carry ANSI escapes, which runewidth would count
// as printable.
func glyphWidth(s string) int { return cells.StringWidth(s) }

// dotGlyph and dotStyle split the status marker in two so its width can be
// taken before the escapes go on.
func dotGlyph(state string) string {
	if state == "unknown" {
		return glyphHollow
	}
	return glyphFilled
}

func dotStyle(state string) lipgloss.Style {
	switch state {
	case "blocked":
		return styleBlocked
	case "working":
		return styleWorking
	default:
		return styleIdle
	}
}

// dot is the project's status marker (D21): red blocked, yellow working,
// green idle. Unknown keeps idle's green but hollows the circle out (D24
// amended) — same "nothing wants you", minus the claim that anything said so.
// The marker shows only while the project has no unread bell (D23).
func dot(state string, bg rowBg) string {
	return bg.style(dotStyle(state)).Render(dotGlyph(state))
}

// bellMark is the unread marker (D23). It stays uncolored on purpose:
// painting it red would blur it back into the blocked dot it stands in for.
func bellMark(bg rowBg) string {
	return bg.plain(glyphBell)
}

// markerGlyph is the raw glyph the marker slot shows (D23): the 🔔 replaces
// the status dot while bells are unread. marker renders exactly this glyph, so
// the width math and the drawing cannot pick different ones.
func markerGlyph(p project) string {
	if unread(p) {
		return glyphBell
	}
	return dotGlyph(aggregate(p))
}

// marker is a project's single marker slot, rendered.
func marker(p project, bg rowBg) string {
	if g := markerGlyph(p); g == glyphBell {
		return bg.plain(g)
	}
	return dot(aggregate(p), bg)
}

// statusBadge words a reported status; a command with nothing reported gets
// the hollow green circle rather than a dim dash (D24), so every command
// carries a circle-or-word state instead of a blank.
func statusBadge(status string, bg rowBg) string {
	switch status {
	case "waiting":
		return bg.style(styleBlocked).Render("waiting")
	case "working":
		return bg.style(styleWorking).Render("working")
	case "done":
		return bg.style(styleIdle).Render("done")
	default:
		return dot("unknown", bg)
	}
}

func (m model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}
	content := m.render(m.width, m.height)
	v := tea.NewView(content)
	v.AltScreen = true
	// Cell motion is enough for D24's click-to-select; all-motion would add
	// hover events the mock has no use for.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// render carves the (w, h) rectangle sequentially per the active def and
// paints components into the slices; whatever remains is the main region.
func (m model) render(w, h int) string {
	if !m.shown {
		return m.renderMain(w, h)
	}
	return m.carve(defs[m.defIdx].entries, w, h)
}

func (m model) carve(entries []entry, w, h int) string {
	if len(entries) == 0 {
		return m.renderMain(w, h)
	}
	e, rest := entries[0], entries[1:]
	horizontal := e.edge == "left" || e.edge == "right"
	// Percent resolves against the remaining rectangle (Q27 default).
	size := e.cells
	if e.pct > 0 {
		if horizontal {
			size = w * e.pct / 100
		} else {
			size = h * e.pct / 100
		}
	}
	if horizontal {
		size = min(size, w-1)
		pane := m.renderComponent(e.component, size, h, false)
		remainder := m.carve(rest, w-size, h)
		if e.edge == "left" {
			return lipgloss.JoinHorizontal(lipgloss.Top, pane, remainder)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, remainder, pane)
	}
	size = min(size, h-1)
	pane := m.renderComponent(e.component, w, size, true)
	remainder := m.carve(rest, w, h-size)
	if e.edge == "top" {
		return lipgloss.JoinVertical(lipgloss.Left, pane, remainder)
	}
	return lipgloss.JoinVertical(lipgloss.Left, remainder, pane)
}

func (m model) renderComponent(name string, w, h int, row bool) string {
	var body string
	switch name {
	case "switcher":
		if row {
			body = m.renderSwitcherRow()
		} else {
			body = m.renderSwitcherColumn(w, h)
		}
	case "statusbar":
		body = m.renderStatusbar(w)
	default:
		body = styleDim.Render("[" + name + "]")
	}
	return lipgloss.Place(w, h, lipgloss.Left, lipgloss.Top, body)
}

// renderSwitcherColumn is the docked side-bar form: a grouped list (D20) —
// each project heads its group with its one marker (🔔 while unread, else the
// status dot; D21, D23), every command's title listed under it. The selected
// group is highlighted as one solid background block, not just its head line;
// the focused group, when it is not the selected one, gets the dim block.
// The list scrolls inside the pane (D25); the header and the legend/hint
// footer are pinned, so only the group rows move.
func (m model) renderSwitcherColumn(w, h int) string {
	lines := m.groupLines(w)
	l := m.columnLayout(h)
	var b strings.Builder
	b.WriteString(styleFrame.Render("projects"))
	b.WriteByte('\n')
	if l.scrolling {
		b.WriteString(moreHint("↑", l.off))
		b.WriteByte('\n')
	}
	for _, line := range lines[l.off:min(l.off+l.avail, len(lines))] {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if l.scrolling {
		b.WriteString(moreHint("↓", len(lines)-l.off-l.avail))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(m.renderLegend())
	b.WriteString(styleDim.Render("j/k move · enter go · b ring · click/wheel"))
	return lipgloss.NewStyle().MaxWidth(w).MaxHeight(h).Render(b.String())
}

// groupLines renders the scrollable region: every project's head line
// followed by one line per command, each already padded to w so a
// highlighted group forms a solid block.
func (m model) groupLines(w int) []string {
	lines := make([]string, 0, m.groupLineCount())
	for i, p := range m.projects {
		bg := bgFor(i == m.selected, i == m.focused)
		// The marker sits in a fixed-width slot, so heads line up with each
		// other whichever marker shows; the gap comes off the glyph's measured
		// width, not an assumed one. Its margin (D24) leads, and carries the
		// row background like the rest.
		mk := marker(p, bg)
		gap := strings.Repeat(" ", markerSlot-glyphWidth(markerGlyph(p)))
		head := bg.plain(markerMargin) + mk +
			bg.style(lipgloss.NewStyle().Bold(true)).Render(gap+p.name)
		lines = append(lines, padLine(head, w, bg))
		for _, c := range bucketSorted(p.commands) {
			title := c.title
			if title == "" {
				title = "(no title)"
			}
			// The app name carries the derived weak color (D26); the title
			// stays fainter still, and the status keeps its semantic color.
			line := bg.style(m.weakStyle()).Render(fmt.Sprintf("    %-9s ", c.name)) +
				statusBadge(c.status, bg) +
				bg.style(styleDim).Render(" · "+title)
			lines = append(lines, padLine(line, w, bg))
		}
	}
	return lines
}

// moreHint marks rows hidden above or below the viewport; blank when there
// are none, so the viewport keeps a stable height either way.
func moreHint(arrow string, n int) string {
	if n <= 0 {
		return ""
	}
	return styleDim.Render(fmt.Sprintf("%s %d more", arrow, n))
}

// markerSlot is where a group head's name starts, measured from the marker:
// the widest marker (🔔) plus the gap D26 tightened to one. Its width is taken
// from the glyph rather than written down, so a terminal that measures the
// bell differently moves the whole column instead of tearing it. D24's extra
// cell of margin sits to the marker's left, see markerMargin. With the margin
// it puts head names in the command rows' text column.
var markerSlot = glyphWidth(glyphBell) + 1

// markerMargin is D24's margin, one space to the left of the marker. It
// carries the row background like every other piece of the line.
const markerMargin = " "

// renderLegend spells out the marker vocabulary, bell first so it reads as
// one slot's alternatives (D23). Filled and hollow green are separate entries
// (D24 amended); three per line keeps every line inside the 42-cell def.
func (m model) renderLegend() string {
	entries := []struct{ mark, glyph, label string }{
		{bellMark(bgNone), glyphBell, " unread"},
		{dot("blocked", bgNone), glyphFilled, " blocked"},
		{dot("working", bgNone), glyphFilled, " working"},
		{dot("idle", bgNone), glyphFilled, " idle"},
		{dot("unknown", bgNone), glyphHollow, " unknown"},
	}
	const (
		col     = 12
		perLine = 3
	)
	var b strings.Builder
	for i, e := range entries {
		b.WriteString(e.mark)
		b.WriteString(styleDim.Render(e.label))
		if i%perLine == perLine-1 || i == len(entries)-1 {
			b.WriteByte('\n')
			continue
		}
		b.WriteString(strings.Repeat(" ", col-glyphWidth(e.glyph)-glyphWidth(e.label)))
	}
	return b.String()
}

// padLine right-pads to the column width so a highlighted group's background
// forms a solid block.
func padLine(line string, w int, bg rowBg) string {
	pad := w - lipgloss.Width(line)
	if pad <= 0 {
		return line
	}
	return line + bg.plain(strings.Repeat(" ", pad))
}

// renderSwitcherRow is the docked bottom-launcher form: one marker (🔔 while
// unread, else the status dot; D23) + name side by side; grouped titles need
// vertical space, so this form shows aggregates only (D20). Nothing lines up
// in a row, so the marker needs no padded slot here — just D24's margin.
//
// This form does not scroll (D25 covers the column only): with more projects
// than fit, the tail is simply clipped by the pane. Paging it needs a
// horizontal viewport, which is its own review round.
func (m model) renderSwitcherRow() string {
	items := make([]string, 0, len(m.projects))
	for i := range m.projects {
		items = append(items, m.rowItem(i))
	}
	sep := styleDim.Render("│")
	return styleFrame.Render("⏵ ") + strings.Join(items, sep) + "\n" +
		styleDim.Render("  j/k move · enter go · b ring · click go · f hide · c cycle def")
}

// rowItem renders one bottom-row entry. Hit-testing measures the very strings
// the row is built from (see projectAtRow), so the two cannot drift apart.
func (m model) rowItem(i int) string {
	bg := bgFor(i == m.selected, i == m.focused)
	return bg.plain(" "+markerMargin) + marker(m.projects[i], bg) +
		bg.plain(" "+m.projects[i].name+" ")
}

// renderStatusbar reads left to right as "where you are, then what wants you,
// then how to drive it": the focused project with its own marker, the counts
// across every project, and the def plus key hints pushed to the right edge.
// Every piece carries the bar's background, so the marker keeps its status
// color inside the block instead of being flattened by one outer style.
func (m model) renderStatusbar(w int) string {
	blocked, unreadN := 0, 0
	for _, p := range m.projects {
		if aggregate(p) == "blocked" {
			blocked++
		}
		if unread(p) {
			unreadN++
		}
	}
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color("231"))
	focused := m.projects[m.focused]
	left := bgSelected.plain(" ") + marker(focused, bgSelected) +
		bgSelected.style(bar.Bold(true)).Render(" "+focused.name)
	// Blocked is status-derived; unread bells are counted separately (D22).
	counts := fmt.Sprintf("  %d blocked", blocked)
	if unreadN > 0 {
		counts += fmt.Sprintf(" · %d🔔", unreadN)
	}
	left += bgSelected.style(bar).Render(counts)
	right := bgSelected.style(bar).Render(
		fmt.Sprintf("%s · f hide · c cycle · q quit ", defs[m.defIdx].name))
	pad := w - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		// Narrow pane: the hints go before the def name, and the def name
		// before the left side — where you are outranks how to drive it.
		right = bgSelected.style(bar).Render(defs[m.defIdx].name + " ")
		pad = w - lipgloss.Width(left) - lipgloss.Width(right)
	}
	if pad < 1 {
		bare := lipgloss.NewStyle().MaxWidth(w).Render(left)
		return bare + bgSelected.plain(strings.Repeat(" ", max(w-lipgloss.Width(bare), 0)))
	}
	return left + bgSelected.plain(strings.Repeat(" ", pad)) + right
}

// renderMain fakes the focused project's mux layout in the leftover space.
func (m model) renderMain(w, h int) string {
	p := m.projects[m.focused]
	var b strings.Builder
	for _, c := range p.commands {
		title := c.title
		if title == "" {
			title = "(no title)"
		}
		fmt.Fprintf(&b, "┌─ %s — %s · %s\n", c.name, statusBadge(c.status, bgNone),
			styleDim.Render(title))
		b.WriteString(styleDim.Render("│  … pane output …"))
		b.WriteByte('\n')
	}
	if !m.shown {
		b.WriteByte('\n')
		b.WriteString(styleDim.Render("frame hidden — f to show"))
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Width(w - 2).
		Height(h - 2)
	title := styleFrame.Render(" " + p.name + " (main region) ")
	return box.Render(title + "\n" + b.String())
}

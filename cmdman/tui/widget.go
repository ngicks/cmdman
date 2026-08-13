package tui

import (
	"context"
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// Widget names a single-view widget mode: one view filling the pane, with no
// tab bar and no tab switching. Widgets are the entry point a frame def's
// `component:` resolves to (`cmdman tui widget <name>`), and each one is its own
// CLI subcommand.
type Widget int

const (
	// WidgetNone is the zero value and names no widget: the full TUI runs.
	WidgetNone Widget = iota
	// WidgetSwitcher is the docked project switcher: projects with their
	// commands listed under them.
	WidgetSwitcher
	// WidgetStatusbar is the one-line status bar.
	WidgetStatusbar
	// WidgetLauncher is the quick-launch selector: locations left, their compose
	// projects right. It is the view a mux key binding summons as a popup (D3).
	WidgetLauncher
)

// widgetDefs is the single source of truth for the widget modes and their CLI
// tokens — the `cmdman tui widget <name>` subcommand names, which are also the
// built-in component names a frame def references. WidgetNone is deliberately
// absent: it names no widget.
var widgetDefs = []struct {
	widget Widget
	key    string
}{
	{WidgetSwitcher, "switcher"},
	{WidgetStatusbar, "statusbar"},
	{WidgetLauncher, "launcher"},
}

// WidgetKeys returns the widget CLI tokens in declaration order.
func WidgetKeys() []string {
	keys := make([]string, len(widgetDefs))
	for i, d := range widgetDefs {
		keys[i] = d.key
	}
	return keys
}

// ParseWidget maps a widget CLI token to its Widget. It is the inverse of the
// widgetDefs key column; WidgetNone has no token and never parses.
func ParseWidget(s string) (Widget, error) {
	for _, d := range widgetDefs {
		if d.key == s {
			return d.widget, nil
		}
	}
	return WidgetNone, fmt.Errorf(
		"invalid widget %q: want one of %s", s, strings.Join(WidgetKeys(), ", "))
}

// widgetModel is the restricted single-view model. It shares the Backend, the
// load commands, and the event subscription with the full model, but renders
// exactly one widget and handles only the keys that widget needs.
type widgetModel struct {
	backend Backend
	widget  Widget
	version string
	// noQuit unbinds the quit keys (V6), which is how a widget docked in a
	// frame pane runs: an exiting viewer leaves a hole in the fixture.
	noQuit bool

	// ctx is the program-scoped context used for backend calls, mirroring
	// Model.ctx; tests that drive Update directly may leave it nil.
	ctx context.Context

	width, height int
	altScreen     bool

	// cmds and projs are the last loaded lists, kept raw because groups is
	// their join and either list can arrive first.
	cmds   []CommandInfo
	projs  []ProjectInfo
	groups []projectGroup

	// titles carries when each command's current title was first seen, which is
	// what D20's bucket sort orders by (see titleStamp). now is the clock that
	// stamps them; nil means time.Now, and tests set it to keep the buckets
	// deterministic.
	titles map[string]titleStamp
	now    func() time.Time

	selected int    // index into groups
	cwd      string // normalized working directory for active detection
	status   string // transient error text, shown in place of the hint line

	// bellRead carries the command ids whose bell was resolved by selecting
	// their project (D22). The monitor keeps reporting such a bell as unread —
	// inside it only an attach reads one (D11) — so the switcher remembers what
	// it already showed the user until the monitor's own flag goes down, at
	// which point a bell that rings again is news again.
	bellRead map[string]bool

	events    EventStream
	reloadGen int

	// termFg/termBg are the terminal's own colors (D26), nil until it answers
	// the startup query — and some terminals never do. weak is the app-row
	// shade derived from them.
	termFg, termBg color.Color
	fgDark         bool
	weak           color.Color

	quitting bool
}

// newWidget constructs the single-widget model from the same options the full
// model takes.
func newWidget(opts Options) widgetModel {
	return widgetModel{
		backend:   opts.Backend,
		widget:    opts.Widget,
		version:   opts.Version,
		altScreen: opts.AltScreen,
		noQuit:    opts.NoQuit,
	}
}

func (m widgetModel) bgCtx() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// Init implements tea.Model. Both widgets read the same two listings; only the
// switcher paints app rows, so only it asks the terminal for its palette.
func (m widgetModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		listCommandsCmd(m.bgCtx(), m.backend),
		listProjectsCmd(m.bgCtx(), m.backend),
		subscribeCmd(m.bgCtx(), m.backend),
	}
	if m.widget == WidgetSwitcher {
		cmds = append(cmds, tea.RequestForegroundColor, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m widgetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	case commandsLoadedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("list error: %v", msg.err)
			return m, nil
		}
		m.cmds, m.status = msg.infos, ""
		return m.rebuild(), nil
	case projectsLoadedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("project list error: %v", msg.err)
			return m, nil
		}
		m.projs, m.status = msg.infos, ""
		return m.rebuild(), nil
	case eventsSubscribedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("events: %v", msg.err)
			return m, nil
		}
		m.events = msg.stream
		return m, waitEventCmd(msg.stream)
	case eventSignalMsg:
		return m.onEventSignal(msg)
	case reloadTickMsg:
		if msg.gen != m.reloadGen {
			return m, nil // a newer event arrived; let the latest tick win
		}
		return m, tea.Batch(
			listCommandsCmd(m.bgCtx(), m.backend),
			listProjectsCmd(m.bgCtx(), m.backend),
		)
	case projectSwitchedMsg:
		return m.onProjectSwitched(msg), nil
	case frameHiddenMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("hide frame: %v", msg.err)
		}
		return m, nil
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		return m.clickAt(msg.Y)
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m widgetModel) onEventSignal(msg eventSignalMsg) (tea.Model, tea.Cmd) {
	if msg.closed {
		m.events = nil
		return m, nil // subscription ended; stop waiting
	}
	if msg.err != nil {
		m.status = fmt.Sprintf("events: %v", msg.err)
		return m, waitEventCmd(m.events)
	}
	m.reloadGen++
	return m, tea.Batch(waitEventCmd(m.events), debounceCmd(m.reloadGen))
}

// onKey handles the widget key set: the switcher's cursor keys, the selection
// that takes the client to a project's window (D6), and the collapse gesture
// that takes the whole frame down (V8). The switcher is navigate-only — start,
// stop and kill stay in the full TUI (V6).
func (m widgetModel) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "ctrl+d":
		if m.noQuit {
			// A docked widget has no quit: the pane would stay behind empty (V6).
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "enter":
		return m.switchToSelected()
	case "z":
		if m.widget != WidgetSwitcher {
			return m, nil
		}
		return m, hideFrameCmd(m.bgCtx(), m.backend)
	}
	return m, nil
}

func (m *widgetModel) moveSelection(delta int) {
	if len(m.groups) == 0 {
		m.selected = 0
		return
	}
	m.selected = min(max(m.selected+delta, 0), len(m.groups)-1)
}

// clickAt selects the group the pointer landed on and switches to it, which is
// what enter does on that group (D24) — a click is a selection, and a selection
// is the switch.
func (m widgetModel) clickAt(y int) (tea.Model, tea.Cmd) {
	if m.widget != WidgetSwitcher {
		return m, nil
	}
	i, ok := m.groupAt(y)
	if !ok {
		return m, nil
	}
	m.selected = i
	return m.switchToSelected()
}

// switchToSelected takes the client to the selected project's window and reads
// that project's bells: selecting a project through the switcher is what
// resolves them (D22).
func (m widgetModel) switchToSelected() (tea.Model, tea.Cmd) {
	if m.widget != WidgetSwitcher {
		// Every widget loads the same listings, but only the switcher paints a
		// cursor over them: elsewhere there is no selection to act on.
		return m, nil
	}
	g, ok := m.selectedGroup()
	if !ok {
		return m, nil
	}
	if g.identity == "" {
		// Nothing to address: a project the backend could not stamp an identity
		// for has no window this switcher could be looking at.
		m.status = fmt.Sprintf("%s: no window to switch to", groupLabel(g))
		return m, nil
	}
	m = m.readBells(m.selected)
	return m, switchProjectCmd(m.bgCtx(), m.backend, g.identity, groupLabel(g))
}

// onProjectSwitched reports a switch. Success needs no word — the client is
// looking at the project's window now — so it only clears whatever the last
// failure left behind.
func (m widgetModel) onProjectSwitched(msg projectSwitchedMsg) widgetModel {
	m.status = ""
	if msg.err != nil {
		m.status = fmt.Sprintf("switch to %s: %v", msg.name, msg.err)
	}
	return m
}

// readBells marks the group's unread bells read, both in the rows on screen and
// in the set that survives the next reload (see widgetModel.bellRead).
func (m widgetModel) readBells(i int) widgetModel {
	if i < 0 || i >= len(m.groups) {
		return m
	}
	if m.bellRead == nil {
		m.bellRead = map[string]bool{}
	}
	for j, c := range m.groups[i].commands {
		if !c.bell {
			continue
		}
		m.bellRead[c.id] = true
		m.groups[i].commands[j].bell = false
	}
	return m
}

// applyBellRead re-suppresses the bells an earlier selection resolved and
// forgets the ones the monitor has since cleared itself, so the acknowledgement
// covers the bell it was given for and not the next one.
func (m widgetModel) applyBellRead() widgetModel {
	if len(m.bellRead) == 0 {
		return m
	}
	read := make(map[string]bool, len(m.bellRead))
	for i := range m.groups {
		for j, c := range m.groups[i].commands {
			if !c.bell || !m.bellRead[c.id] {
				continue
			}
			read[c.id] = true
			m.groups[i].commands[j].bell = false
		}
	}
	m.bellRead = read
	return m
}

// groupLabel names a project group in a message the user reads.
func groupLabel(g projectGroup) string {
	if g.name == "" {
		return "(unnamed)"
	}
	return g.name
}

// rebuild re-joins the two listings into the switcher's groups, keeping the
// selection on the project it was on across a reload.
func (m widgetModel) rebuild() widgetModel {
	if m.backend != nil {
		m.cwd = m.backend.Cwd()
	}
	var prev string
	if g, ok := m.selectedGroup(); ok {
		prev = g.key()
	}
	m.titles = m.stampTitles()
	m.groups = switcherGroups(m.projs, m.cmds, m.cwd, m.titles)
	m.selected = 0
	for i, g := range m.groups {
		if g.key() == prev {
			m.selected = i
			break
		}
	}
	return m.applyBellRead()
}

// stampTitles carries every still-current title stamp forward and dates the
// rest at now: a command whose title is unchanged keeps the time it was first
// seen with it, one that retitled starts a new bucket, and one that vanished
// drops out with the map it is rebuilt into.
func (m widgetModel) stampTitles() map[string]titleStamp {
	now := time.Now
	if m.now != nil {
		now = m.now
	}
	at := now()
	out := make(map[string]titleStamp, len(m.cmds))
	for _, ci := range m.cmds {
		if ci.Title == "" {
			continue
		}
		if prev, ok := m.titles[ci.ID]; ok && prev.title == ci.Title {
			out[ci.ID] = prev
			continue
		}
		out[ci.ID] = titleStamp{title: ci.Title, at: at}
	}
	return out
}

func (m widgetModel) selectedGroup() (projectGroup, bool) {
	if m.selected < 0 || m.selected >= len(m.groups) {
		return projectGroup{}, false
	}
	return m.groups[m.selected], true
}

// View implements tea.Model.
func (m widgetModel) View() tea.View {
	v := tea.NewView(m.viewContent())
	v.AltScreen = m.altScreen
	if m.widget == WidgetSwitcher {
		// Clicking a project is one of its two selection gestures (D24); the
		// statusbar has nothing to point at.
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// size is the pane the widget draws into, with the fallback a model that has
// not been told its size yet renders at. Hit-testing measures with the same
// ruler the render used.
func (m widgetModel) size() (int, int) {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func (m widgetModel) viewContent() string {
	if m.quitting {
		return ""
	}
	width, height := m.size()
	if m.widget == WidgetStatusbar {
		return m.renderStatusbar(width)
	}
	return m.renderSwitcher(width, height)
}

// --- shared widget rendering ------------------------------------------------

// cells measures raw glyphs with East-Asian *Ambiguous* characters pinned to
// one cell. That pin is load-bearing, not tidiness: ● and ○ are Ambiguous, so
// under a CJK locale go-runewidth's package default calls them 2 while lipgloss
// (uniseg), which actually draws them, renders 1 — measuring a gap with one
// ruler and drawing the row with the other tears the columns apart. An explicit
// Condition also keeps the measurement independent of the ambient locale.
// Strings that have already been rendered carry ANSI escapes and are measured
// with lipgloss.Width instead, which knows to skip them.
var cells = &runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}

// glyphWidth measures a raw (unrendered) glyph.
func glyphWidth(s string) int { return cells.StringWidth(s) }

// padCells pads or truncates a raw string to exactly w cells.
func padCells(s string, w int) string {
	if d := w - cells.StringWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return cells.Truncate(s, w, "")
}

// truncateLeftCells cuts a raw string to at most w cells keeping its tail, with
// a leading ellipsis where it cut. Cells, not runes: a tail of double-width
// glyphs cut by rune count comes back up to twice as wide as the column that
// asked for it, which overruns the row and turns the padding that follows into
// a negative repeat. A wide rune that no longer fits is dropped whole, so the
// result may land a cell short of w rather than a cell over it.
func truncateLeftCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if cells.StringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	width, i := 0, len(r)
	for i > 0 {
		cw := cells.RuneWidth(r[i-1])
		if width+cw > w-1 { // one cell is the ellipsis'
			break
		}
		width += cw
		i--
	}
	return "…" + string(r[i:])
}

// The marker glyphs. They are named because their widths feed the layout, and
// those widths are measured rather than assumed — see markerSlot. Filled ● is
// a reported state (idle/working/blocked, told apart by color); hollow ○ is
// "nothing reported at all" (D24 amended) — same color, different shape,
// because color alone cannot carry the reported-vs-not distinction.
const (
	glyphBell   = "🔔"
	glyphFilled = "●"
	glyphHollow = "○"
)

// The reported-status vocabulary (D12) as the backend-neutral CommandInfo
// spells it. They are the rendering of the wire enum, mirrored here rather than
// imported so the model stays exercisable without the service packages.
const (
	statusWorking = "working"
	statusWaiting = "waiting"
	statusDone    = "done"
)

// markerSlot is where a group head's name starts, measured from the marker: the
// widest marker (the bell) plus one space. The width is taken from the glyph
// rather than written down, so a terminal that measures the bell differently
// moves the whole column instead of tearing it — and so the slot does not shift
// when runtime state starts putting a bell in it.
var markerSlot = glyphWidth(glyphBell) + 1

// markerMargin is the one space to the left of the marker.
const markerMargin = " "

// colorWeakBlock is the second cursor's block: dark enough to read as "the
// cursor is here too" without competing with the focused pane's indigo.
var colorWeakBlock = lipgloss.Color("237")

// rowBg is the background a rendered row carries. A selected switcher group is
// one solid block spanning its head and its command rows, so every piece of
// every line in it renders with the background rather than one outer style
// being laid over pre-colored text. bgWeak is the launcher's second cursor: a
// two-pane view shows where both panes stand, so the pane without the keyboard
// keeps a fainter block (D31).
type rowBg int

const (
	bgNone rowBg = iota
	bgWeak
	bgAccent
)

func (b rowBg) style(st lipgloss.Style) lipgloss.Style {
	switch b {
	case bgAccent:
		return st.Background(colorBorder)
	case bgWeak:
		return st.Background(colorWeakBlock)
	}
	return st
}

// plain renders s carrying only the row background.
func (b rowBg) plain(s string) string {
	return b.style(lipgloss.NewStyle()).Render(s)
}

var (
	styleWidgetTitle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleWidgetHead  = lipgloss.NewStyle().Bold(true)
	styleWidgetBar   = lipgloss.NewStyle().Foreground(colorOnAcc)
	// The traffic-light marker palette (D21): green nothing wants you, yellow
	// something is working, red something is blocked on you. The status words in
	// command rows share it, so a row and its project's dot say the same thing
	// twice rather than in two vocabularies. They are basic ANSI colors like the
	// rest of this TUI's markers (view.go's styleMark*), so they follow the
	// user's own theme.
	styleMarkerIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleMarkerWork    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleMarkerBlocked = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// reportedStatusStyle colors a reported status word or the dot standing for it.
// Anything unreported keeps idle's green: "nothing wants you" is what both
// mean, and the glyph — hollow rather than filled — carries the difference.
func reportedStatusStyle(status string) lipgloss.Style {
	switch status {
	case statusWaiting:
		return styleMarkerBlocked
	case statusWorking:
		return styleMarkerWork
	default:
		return styleMarkerIdle
	}
}

// reportedStatusBadge renders a command's reported status: the word when it
// reported one, else the hollow circle standing for "nothing said so far", so
// every command carries a circle-or-word state instead of a blank (D24).
func reportedStatusBadge(status string, bg rowBg) string {
	if status == "" {
		return bg.style(styleMarkerIdle).Render(glyphHollow)
	}
	return bg.style(reportedStatusStyle(status)).Render(status)
}

// rowStateBadge is the one state word a command row carries. A live run shows
// what it reported about itself; anything else shows its lifecycle state, which
// is the truthful signal for a run that is over or not yet begun — an exited
// command must never show its last report (D13).
func rowStateBadge(c commandRow, bg rowBg) string {
	if liveReport(c) {
		return reportedStatusBadge(c.status, bg)
	}
	label := displayLabel(c.state, c.exitCode)
	if c.pending != "" {
		label = c.pending + "…"
	}
	return bg.style(statusStyle(c.state, c.pending)).Render(label)
}

// padLine truncates and right-pads a rendered line to exactly w cells, so a
// highlighted group forms a solid block and no line overflows its pane.
func padLine(line string, w int, bg rowBg) string {
	line = ansi.Truncate(line, w, "")
	if pad := w - lipgloss.Width(line); pad > 0 {
		line += bg.plain(strings.Repeat(" ", pad))
	}
	return line
}

// weakRatio is how far the app rows travel from the letter color toward the
// background. Much past this they stop being readable on low-contrast themes.
const weakRatio = 0.55

// deriveWeak recomputes the app-row shade from whatever the terminal has
// reported so far, so the two answers may arrive in either order.
func (m widgetModel) deriveWeak() widgetModel {
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

// weakStyle is the command rows' foreground: the terminal's own letter color
// pulled toward its background, so a group reads as bright head plus subdued
// detail on light and dark terminals alike. Faint is the fallback for terminals
// that never answer the query.
func (m widgetModel) weakStyle() lipgloss.Style {
	if m.weak == nil {
		return styleActive
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

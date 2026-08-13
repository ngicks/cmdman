package tui

import (
	"context"
	"fmt"
	"image/color"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// widgetModel is the restricted single-view model. It shares the Backend, the
// load commands, and the event subscription with the full model, but renders
// exactly one widget and handles only the keys that widget needs.
type widgetModel struct {
	backend core.Backend
	widget  core.Widget
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
	cmds   []core.CommandInfo
	projs  []core.ProjectInfo
	groups []core.ProjectGroup

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

	events    core.EventStream
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
func newWidget(opts core.Options) widgetModel {
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
		core.ListCommandsCmd(m.bgCtx(), m.backend),
		core.ListProjectsCmd(m.bgCtx(), m.backend),
		core.SubscribeCmd(m.bgCtx(), m.backend),
	}
	if m.widget == core.WidgetSwitcher {
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
	case core.CommandsLoadedMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("list error: %v", msg.Err)
			return m, nil
		}
		m.cmds, m.status = msg.Infos, ""
		return m.rebuild(), nil
	case core.ProjectsLoadedMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("project list error: %v", msg.Err)
			return m, nil
		}
		m.projs, m.status = msg.Infos, ""
		return m.rebuild(), nil
	case core.EventsSubscribedMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("events: %v", msg.Err)
			return m, nil
		}
		m.events = msg.Stream
		return m, core.WaitEventCmd(msg.Stream)
	case core.EventSignalMsg:
		return m.onEventSignal(msg)
	case core.ReloadTickMsg:
		if msg.Gen != m.reloadGen {
			return m, nil // a newer event arrived; let the latest tick win
		}
		return m, tea.Batch(
			core.ListCommandsCmd(m.bgCtx(), m.backend),
			core.ListProjectsCmd(m.bgCtx(), m.backend),
		)
	case core.ProjectSwitchedMsg:
		return m.onProjectSwitched(msg), nil
	case core.FrameHiddenMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("hide frame: %v", msg.Err)
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

func (m widgetModel) onEventSignal(msg core.EventSignalMsg) (tea.Model, tea.Cmd) {
	if msg.Closed {
		m.events = nil
		return m, nil // subscription ended; stop waiting
	}
	if msg.Err != nil {
		m.status = fmt.Sprintf("events: %v", msg.Err)
		return m, core.WaitEventCmd(m.events)
	}
	m.reloadGen++
	return m, tea.Batch(core.WaitEventCmd(m.events), core.DebounceCmd(m.reloadGen))
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
		if m.widget != core.WidgetSwitcher {
			return m, nil
		}
		return m, core.HideFrameCmd(m.bgCtx(), m.backend)
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
	if m.widget != core.WidgetSwitcher {
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
	if m.widget != core.WidgetSwitcher {
		// Every widget loads the same listings, but only the switcher paints a
		// cursor over them: elsewhere there is no selection to act on.
		return m, nil
	}
	g, ok := m.selectedGroup()
	if !ok {
		return m, nil
	}
	if g.Identity == "" {
		// Nothing to address: a project the backend could not stamp an identity
		// for has no window this switcher could be looking at.
		m.status = fmt.Sprintf("%s: no window to switch to", groupLabel(g))
		return m, nil
	}
	m = m.readBells(m.selected)
	return m, core.SwitchProjectCmd(m.bgCtx(), m.backend, g.Identity, groupLabel(g))
}

// onProjectSwitched reports a switch. Success needs no word — the client is
// looking at the project's window now — so it only clears whatever the last
// failure left behind.
func (m widgetModel) onProjectSwitched(msg core.ProjectSwitchedMsg) widgetModel {
	m.status = ""
	if msg.Err != nil {
		m.status = fmt.Sprintf("switch to %s: %v", msg.Name, msg.Err)
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
	for j, c := range m.groups[i].Commands {
		if !c.Bell {
			continue
		}
		m.bellRead[c.ID] = true
		m.groups[i].Commands[j].Bell = false
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
		for j, c := range m.groups[i].Commands {
			if !c.Bell || !m.bellRead[c.ID] {
				continue
			}
			read[c.ID] = true
			m.groups[i].Commands[j].Bell = false
		}
	}
	m.bellRead = read
	return m
}

// groupLabel names a project group in a message the user reads.
func groupLabel(g core.ProjectGroup) string {
	if g.Name == "" {
		return "(unnamed)"
	}
	return g.Name
}

// rebuild re-joins the two listings into the switcher's groups, keeping the
// selection on the project it was on across a reload.
func (m widgetModel) rebuild() widgetModel {
	if m.backend != nil {
		m.cwd = m.backend.Cwd()
	}
	var prev string
	if g, ok := m.selectedGroup(); ok {
		prev = g.Key()
	}
	m.titles = m.stampTitles()
	m.groups = switcherGroups(m.projs, m.cmds, m.cwd, m.titles)
	m.selected = 0
	for i, g := range m.groups {
		if g.Key() == prev {
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

func (m widgetModel) selectedGroup() (core.ProjectGroup, bool) {
	if m.selected < 0 || m.selected >= len(m.groups) {
		return core.ProjectGroup{}, false
	}
	return m.groups[m.selected], true
}

// View implements tea.Model.
func (m widgetModel) View() tea.View {
	v := tea.NewView(m.viewContent())
	v.AltScreen = m.altScreen
	if m.widget == core.WidgetSwitcher {
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
	if m.widget == core.WidgetStatusbar {
		return m.renderStatusbar(width)
	}
	return m.renderSwitcher(width, height)
}

// deriveWeak recomputes the app-row shade from whatever the terminal has
// reported so far, so the two answers may arrive in either order.
func (m widgetModel) deriveWeak() widgetModel {
	if weak := core.DeriveWeak(m.termFg, m.termBg, m.fgDark); weak != nil {
		m.weak = weak
	}
	return m
}

// weakStyle is the command rows' foreground: the terminal's own letter color
// pulled toward its background, so a group reads as bright head plus subdued
// detail on light and dark terminals alike.
func (m widgetModel) weakStyle() lipgloss.Style { return core.WeakStyle(m.weak) }

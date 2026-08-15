// Package switcher implements the docked project switcher: every project
// heading its group with one marker slot, its commands listed under it, and
// enter or a click taking the client to that project's window (D6, D24).
//
// It is a single-view model of its own — one pane, one list, and only the keys
// that list needs — rather than a branch of the model that hosts the tabs.
// Everything it shares with the rest of the TUI lives in
// cmdman/tui/internal/core.
package switcher

import (
	"cmp"
	"context"
	"fmt"
	"image/color"
	"math"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// Model is the restricted single-view model. It shares the Backend, the
// load commands, and the event subscription with the full model, but renders
// one docked column and handles only the keys that column needs.
type Model struct {
	backend core.Backend
	// noQuit unbinds the quit keys (V6), which is how a widget docked in a
	// frame pane runs: an exiting viewer leaves a hole in the fixture.
	noQuit bool

	// ctx is the program-scoped context used for backend calls, as the root
	// model holds it; tests that drive Update directly may leave it nil.
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

// New constructs the switcher from the same options the full model takes.
// Options.Widget is not read here: calling New is what names the widget, so an
// Options carrying no widget still builds one.
func New(ctx context.Context, opts core.Options) Model {
	return Model{
		ctx:       ctx,
		backend:   opts.Backend,
		altScreen: opts.AltScreen,
		noQuit:    opts.NoQuit,
	}
}

func (m Model) bgCtx() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// Init implements tea.Model: the two listings the groups are joined from, the
// event subscription that reloads them, and the terminal's own colors, which
// the command rows are shaded against (D26).
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		core.ListCommandsCmd(m.bgCtx(), m.backend),
		core.ListProjectsCmd(m.bgCtx(), m.backend),
		core.SubscribeCmd(m.bgCtx(), m.backend),
		tea.RequestForegroundColor,
		tea.RequestBackgroundColor,
	)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m Model) onEventSignal(msg core.EventSignalMsg) (tea.Model, tea.Cmd) {
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
// that takes the whole frame down (V8). A selection lands in a window and
// nothing more — start, stop and kill stay in the full TUI (V6).
func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		return m, core.HideFrameCmd(m.bgCtx(), m.backend)
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	if len(m.groups) == 0 {
		m.selected = 0
		return
	}
	m.selected = min(max(m.selected+delta, 0), len(m.groups)-1)
}

// clickAt selects the group the pointer landed on and switches to it, which is
// what enter does on that group (D24) — a click is a selection, and a selection
// is the switch.
func (m Model) clickAt(y int) (tea.Model, tea.Cmd) {
	i, ok := m.groupAt(y)
	if !ok {
		return m, nil
	}
	m.selected = i
	return m.switchToSelected()
}

// switchToSelected takes the client to the selected project's window — built
// for it first when none is up — and reads that project's bells: selecting a
// project through the switcher is what resolves them (D22).
func (m Model) switchToSelected() (tea.Model, tea.Cmd) {
	g, ok := m.selectedGroup()
	if !ok {
		return m, nil
	}
	if g.Identity == "" {
		// Nothing to address: a project the backend could not stamp an identity
		// for has no window this switcher could look at, and none it could build
		// one for either — the stamp is what a window would be created under.
		m.status = fmt.Sprintf("%s: no window to switch to", groupLabel(g))
		return m, nil
	}
	m = m.readBells(m.selected)
	target := core.SwitchTarget{Identity: g.Identity, WorkDir: g.Workdir, Project: g.Name}
	return m, core.SwitchProjectCmd(m.bgCtx(), m.backend, target, groupLabel(g))
}

// onProjectSwitched reports a switch. Success needs no word — the client is
// looking at the project's window now — so it only clears whatever the last
// failure left behind.
func (m Model) onProjectSwitched(msg core.ProjectSwitchedMsg) Model {
	m.status = ""
	if msg.Err != nil {
		m.status = fmt.Sprintf("switch to %s: %v", msg.Name, msg.Err)
	}
	return m
}

// readBells marks the group's unread bells read, both in the rows on screen and
// in the set that survives the next reload (see Model.bellRead).
func (m Model) readBells(i int) Model {
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
func (m Model) applyBellRead() Model {
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
func (m Model) rebuild() Model {
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
func (m Model) stampTitles() map[string]titleStamp {
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

// switcherGroups joins the global project list (the ListProjects merge:
// store-known projects, never-run named defs, and the project discovered in the
// working directory) with the command list, so a project with no commands still
// gets a group and a running command lands under its project's head.
//
// Commands are matched on (workdir, project name) — both listings normalize the
// path the same way — falling back to the project name alone for a ProjectInfo
// carrying no workdir, which is how a never-run named def arrives. A command
// group no project entry claims is appended, so a project the listing missed is
// still on screen; standalone commands (no compose project) are dropped, having
// no project window to switch to.
func switcherGroups(
	projs []core.ProjectInfo,
	cmds []core.CommandInfo,
	cwd string,
	titles map[string]titleStamp,
) []core.ProjectGroup {
	cmdGroups := core.GroupFromInfos(cmds)
	claimed := make([]bool, len(cmdGroups))
	byKey := make(map[string]int, len(cmdGroups))
	byName := make(map[string][]int, len(cmdGroups))
	for i, g := range cmdGroups {
		if g.Name == "" {
			claimed[i] = true
			continue
		}
		byKey[g.Key()] = i
		byName[g.Name] = append(byName[g.Name], i)
	}

	out := make([]core.ProjectGroup, 0, len(projs)+len(cmdGroups))
	for _, p := range projs {
		g := core.ProjectGroup{Name: p.Name, Workdir: p.Workdir, Identity: p.Identity}
		if i, ok := matchCommandGroup(p, byKey, byName, claimed); ok {
			claimed[i] = true
			g.Commands = cmdGroups[i].Commands
			if g.Workdir == "" {
				g.Workdir = cmdGroups[i].Workdir
			}
		}
		out = append(out, g)
	}
	for i, g := range cmdGroups {
		if !claimed[i] {
			out = append(out, g)
		}
	}

	for i := range out {
		out[i].Active = out[i].Workdir != "" && out[i].Workdir == cwd
		bucketSort(out[i].Commands, titles)
	}
	slices.SortStableFunc(out, func(a, b core.ProjectGroup) int {
		if a.Active != b.Active {
			return core.BoolFirst(a.Active)
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// matchCommandGroup finds the command group belonging to a project entry: the
// exact (workdir, name) group, or — for an entry with no workdir — any
// unclaimed group of that name. An entry that carries a workdir never claims a
// group from another directory: same-named projects in different directories
// are different projects.
func matchCommandGroup(
	p core.ProjectInfo,
	byKey map[string]int,
	byName map[string][]int,
	claimed []bool,
) (int, bool) {
	if i, ok := byKey[p.Workdir+"\x00"+p.Name]; ok && !claimed[i] {
		return i, true
	}
	if p.Workdir != "" {
		return 0, false
	}
	for _, i := range byName[p.Name] {
		if !claimed[i] {
			return i, true
		}
	}
	return 0, false
}

// titleStamp is a command's current title and when it was first seen carrying
// it. The monitor serves no title timestamp, so "when the title changed" is
// observed here: each load compares the title it fetched against the one the
// last load saw. A title that arrived before the TUI started therefore dates
// from the first load, which puts every project's commands in one bucket until
// something actually retitles — the honest answer, since nothing else is known.
type titleStamp struct {
	title string
	at    time.Time
}

// titleBucket chunks title-update times (D20). Two agents retitling every few
// seconds land in the same bucket and order by name instead of trading places
// on every refresh.
const titleBucket = 5 * time.Second

// bucketSort orders a project's commands newest-title-bucket-first, then by
// name (then id) inside a bucket. Commands with no title sort last: "recently
// active floats up" says nothing about a command that never said anything.
func bucketSort(cmds []core.CommandRow, titles map[string]titleStamp) {
	slices.SortFunc(cmds, func(a, b core.CommandRow) int {
		return cmp.Or(
			cmp.Compare(titleBucketOf(titles[b.ID]), titleBucketOf(titles[a.ID])),
			cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.ID, b.ID),
		)
	})
}

// titleBucketOf is the bucket index of a stamp, with the zero stamp (no title
// seen) pinned below every real one rather than converted, since the zero
// time's Unix value is a large negative number that would merely look like one.
func titleBucketOf(s titleStamp) int64 {
	if s.at.IsZero() {
		return math.MinInt64
	}
	return s.at.UnixNano() / int64(titleBucket)
}

func (m Model) selectedGroup() (core.ProjectGroup, bool) {
	if m.selected < 0 || m.selected >= len(m.groups) {
		return core.ProjectGroup{}, false
	}
	return m.groups[m.selected], true
}

// View implements tea.Model.
func (m Model) View() tea.View {
	v := tea.NewView(m.viewContent())
	v.AltScreen = m.altScreen
	// Clicking a project is one of its two selection gestures (D24).
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// size is the pane the widget draws into, with the fallback a model that has
// not been told its size yet renders at. Hit-testing measures with the same
// ruler the render used.
func (m Model) size() (int, int) {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return width, height
}

func (m Model) viewContent() string {
	if m.quitting {
		return ""
	}
	width, height := m.size()
	return m.renderSwitcher(width, height)
}

// deriveWeak recomputes the app-row shade from whatever the terminal has
// reported so far, so the two answers may arrive in either order.
func (m Model) deriveWeak() Model {
	if weak := core.DeriveWeak(m.termFg, m.termBg, m.fgDark); weak != nil {
		m.weak = weak
	}
	return m
}

// weakStyle is the command rows' foreground: the terminal's own letter color
// pulled toward its background, so a group reads as bright head plus subdued
// detail on light and dark terminals alike.
func (m Model) weakStyle() lipgloss.Style { return core.WeakStyle(m.weak) }

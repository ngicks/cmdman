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
	"maps"
	"math"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ngicks/cmdman/cmdman/model"
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

	// titles carries when each command's current title arrived, which is what
	// D20's bucket sort orders by (see titleStamp). now is the clock that stamps
	// them; nil means time.Now, and tests set it to keep the buckets
	// deterministic.
	titles map[string]titleStamp
	now    func() time.Time

	// watcher holds one runtime-state stream per live command and merges their
	// pushes into one channel; runtime is what those pushes said, keyed by
	// command id. A list load carries no runtime state (L3), so the cache is
	// what the rows are dressed from between pushes.
	watcher *core.RuntimeWatcher
	runtime map[string]core.RuntimeStateView

	selected int    // index into groups
	cwd      string // normalized working directory for active detection
	status   string // transient error text, shown in place of the hint line

	// scrollOff is the list line the wheel parked the view on, and scrolled is
	// whether the view is following it rather than the selection. Scrolling away
	// from the cursor is what a wheel is for, so the two questions — what is
	// selected and what is on screen — come apart while the wheel is driving and
	// are put back together by the next keyboard move (see moveSelection).
	scrollOff int
	scrolled  bool

	// pendingDown is the project whose compose teardown is waiting for its y.
	// The question itself is drawn from this rather than written into status: a
	// listing landing while it waits clears status, and a question that went off
	// the screen while the next key still answered it would take the commands of
	// a project nobody was looking at.
	pendingDown core.DownTarget

	// activeIdentity is the mux ownership stamp of the project the caller is
	// sitting in (D3), "" when no probe answered — which is when the active mark
	// falls back to the working directory.
	activeIdentity string

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
		titles:    map[string]titleStamp{},
		watcher:   core.NewRuntimeWatcher(),
		runtime:   map[string]core.RuntimeStateView{},
	}
}

func (m Model) bgCtx() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// Init implements tea.Model: the two listings the groups are joined from, the
// probe naming the project the caller sits in (D3), the event subscription that
// reloads them, the runtime-state receive the rows are kept live by, and the
// terminal's own colors, which the command rows are shaded against (D26).
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		core.ListCommandsCmd(m.bgCtx(), m.backend),
		core.ListProjectsCmd(m.bgCtx(), m.backend),
		core.ActiveIdentityCmd(m.bgCtx(), m.backend),
		core.SubscribeCmd(m.bgCtx(), m.backend),
		armRuntimeWatchCmd(),
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
		// The list is what says which commands exist, so it is also what the held
		// streams and everything cached from them are reconciled against.
		m.watcher.Reconcile(m.bgCtx(), m.backend, msg.Infos)
		m.sweepRuntime()
		return m.rebuild(), nil
	case core.ProjectsLoadedMsg:
		if msg.Err != nil {
			m.status = fmt.Sprintf("project list error: %v", msg.Err)
			return m, nil
		}
		m.projs, m.status = msg.Infos, ""
		return m.rebuild(), nil
	case core.ActiveIdentityLoadedMsg:
		// A probe that did not answer clears the stamp rather than keeping the
		// last one: the window the caller sits in is what the mark claims, and a
		// stale claim is worse than falling back to the working directory.
		m.activeIdentity = ""
		if msg.OK {
			m.activeIdentity = msg.Identity
		}
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
	case runtimeWatchReadyMsg:
		return m, core.WaitRuntimeUpdateCmd(m.watcher)
	case core.RuntimeUpdateMsg:
		return m.onRuntimeUpdate(msg)
	case core.ReloadTickMsg:
		if msg.Gen != m.reloadGen {
			return m, nil // a newer event arrived; let the latest tick win
		}
		return m, tea.Batch(
			core.ListCommandsCmd(m.bgCtx(), m.backend),
			core.ListProjectsCmd(m.bgCtx(), m.backend),
			core.ActiveIdentityCmd(m.bgCtx(), m.backend),
		)
	case core.ProjectSwitchedMsg:
		return m.onProjectSwitched(msg), nil
	case core.ProjectManagerSummonedMsg:
		return m.onProjectManagerSummoned(msg), nil
	case core.MuxDownMsg:
		m.status = msg.Status()
		return m, nil
	case core.ComposeDownMsg:
		m.status = msg.Status()
		return m, nil
	case tea.MouseWheelMsg:
		return m.wheel(msg), nil
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

// --- live runtime state -----------------------------------------------------

// runtimeWatchReadyMsg arms the merged runtime-state receive. Init hands the
// arming over as a message so the receive — which only returns when a monitor
// pushes — is started from an Update arm, the same shape the event subscription
// has (SubscribeCmd → EventsSubscribedMsg → WaitEventCmd).
type runtimeWatchReadyMsg struct{}

func armRuntimeWatchCmd() tea.Cmd {
	return func() tea.Msg { return runtimeWatchReadyMsg{} }
}

// onRuntimeUpdate folds one pushed runtime state into the widget and rearms the
// receive. The watcher's own close is the one message that ends the loop: with
// no id there is no stream left to hear from.
func (m Model) onRuntimeUpdate(msg core.RuntimeUpdateMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Closed && msg.ID == "":
		return m, nil
	case msg.Closed:
		// One command's monitor left an active state. Its row keeps what it last
		// showed until the lifecycle re-list corrects it, and only a later
		// Reconcile that still lists the command live redials.
		return m, core.WaitRuntimeUpdateCmd(m.watcher)
	case msg.Err != nil:
		// A monitor that stopped answering is not the user's problem: the row
		// stays as it was, exactly as it does for a monitor that was never
		// dialable, and the stream's close follows this error.
		return m, core.WaitRuntimeUpdateCmd(m.watcher)
	case !m.listedLive(msg.ID):
		// A straggler buffered before its stream was dropped: caching it would
		// outlive the sweep that already ran.
		return m, core.WaitRuntimeUpdateCmd(m.watcher)
	}
	m.runtime[msg.ID] = msg.State
	m.stampTitle(msg.ID, msg.State.Title)
	// Rebuilt rather than patched: the push is what D20 sorts by, and the bell it
	// may carry is one D22 may already have answered for.
	return m.rebuild(), core.WaitRuntimeUpdateCmd(m.watcher)
}

// stampTitle dates a title at the arrival of the push that carried it (L4). A
// push repeating the title a command already carries keeps its stamp — the
// bucket says when the title changed, not when it was last reported — and a
// cleared title drops out, since a command saying nothing sorts below the ones
// that do.
func (m *Model) stampTitle(id, title string) {
	if title == "" {
		delete(m.titles, id)
		return
	}
	if prev, ok := m.titles[id]; ok && prev.title == title {
		return
	}
	at := time.Now
	if m.now != nil {
		at = m.now
	}
	m.titles[id] = titleStamp{title: title, at: at()}
}

// listedLive reports whether the loaded list has the id in a state whose monitor
// serves a runtime-state stream, which is the gate an arriving push passes to be
// cached at all.
func (m Model) listedLive(id string) bool {
	for _, ci := range m.cmds {
		if ci.ID == id {
			return liveMonitor(ci.State)
		}
	}
	return false
}

// sweepRuntime bounds what the pushes left behind to the ids the freshly loaded
// list still shows with a live monitor. Sweeping against the list rather than
// against the ids a reconcile dropped is what forgets a command whose stream
// ended on its own: the watcher drops such a stream itself, so no later
// reconcile names it, and a run that came back would otherwise be shown wearing
// the last run's title and bell (D13) until its first push.
func (m *Model) sweepRuntime() {
	live := make(map[string]struct{}, len(m.cmds))
	for _, ci := range m.cmds {
		if liveMonitor(ci.State) {
			live[ci.ID] = struct{}{}
		}
	}
	gone := func(id string) bool {
		_, ok := live[id]
		return !ok
	}
	maps.DeleteFunc(m.runtime, func(id string, _ core.RuntimeStateView) bool { return gone(id) })
	maps.DeleteFunc(m.titles, func(id string, _ titleStamp) bool { return gone(id) })
}

// liveMonitor reports whether a listed command's state is one whose monitor
// serves a runtime-state stream — the widget's own copy of what the watcher
// subscribes by, and the one predicate its cached state is kept against.
func liveMonitor(state model.EventType) bool {
	return state == model.EventTypeStarting || state == model.EventTypeRunning
}

// applyRuntime lays the cached pushes over freshly built rows: a list load
// carries none of it (L3), so without this a re-list would blank every title
// until each monitor pushed again.
func applyRuntime(groups []core.ProjectGroup, runtime map[string]core.RuntimeStateView) {
	for gi := range groups {
		cmds := groups[gi].Commands
		for ci := range cmds {
			v, ok := runtime[cmds[ci].ID]
			if !ok {
				continue
			}
			cmds[ci].Title = v.Title
			cmds[ci].Status = v.Status
			cmds[ci].Detail = v.Detail
			cmds[ci].Bell = v.BellUnread
		}
	}
}

// onKey handles the widget key set: the switcher's cursor keys, the selection
// that takes the client to a project's window (D6), the two summons that open
// the project-manager panel — over the cursor's project, or over the one the
// caller is already in — and the two teardowns. A selection lands in a window
// and nothing more — start, stop and kill of single commands stay in the full
// TUI (V6), and the panel a summon opens is a separate program with its own
// keys.
//
// While a compose teardown is waiting to be confirmed the key set is that one
// question: the key answers it and nothing else, so a q that meant "no" cannot
// quit the widget on the way.
func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingDown.Project != "" {
		return m.answerComposeDown(msg.String())
	}
	switch msg.String() {
	case "q", "ctrl+c", "ctrl+d":
		if m.noQuit {
			// A docked widget has no quit: the pane would stay behind empty (V6).
			return m, nil
		}
		m.quitting = true
		// The watcher keeps a stream per live command, and closing it here hands
		// the monitors their disconnects instead of leaving them to process exit.
		_ = m.watcher.Close()
		return m, tea.Quit
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "enter":
		return m.switchToSelected()
	case "m":
		return m.summonSelected()
	case "M":
		return m.summonActive()
	case "d":
		return m.muxDownSelected()
	case "D":
		return m.confirmComposeDown()
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	// The view follows the cursor again from here, wherever the wheel left it: a
	// cursor moving somewhere off screen is a move the user cannot see.
	m.scrolled = false
	if len(m.groups) == 0 {
		m.selected = 0
		return
	}
	m.selected = min(max(m.selected+delta, 0), len(m.groups)-1)
}

// clickAt selects the group the pointer landed on and switches to it, which is
// what enter does on that group (D24) — a click is a selection, and a selection
// is the switch.
//
// A click also takes back a teardown waiting to be confirmed: the click is
// about to move the cursor, and a question left standing over another project
// would be answered about the wrong one.
func (m Model) clickAt(y int) (tea.Model, tea.Cmd) {
	m.pendingDown = core.DownTarget{}
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

// summonSelected opens the project-manager panel over the selected project
// (D7/D9). The cursor addresses a whole group, head line and command rows
// alike, so the project is the same one enter would switch to whichever of its
// lines the cursor sits on.
func (m Model) summonSelected() (tea.Model, tea.Cmd) {
	g, ok := m.selectedGroup()
	if !ok {
		return m, nil
	}
	return m.summonGroup(g, "no project to manage here")
}

// summonActive is `M`: the same panel over the project the caller is already in
// (the active mark, D3), wherever the cursor happens to be. Looking at another
// project's rows is the ordinary state of the switcher — that is what the list
// is for — and managing the project you are sitting in should not cost the
// cursor its place.
func (m Model) summonActive() (tea.Model, tea.Cmd) {
	g, ok := m.activeGroup()
	if !ok {
		m.status = "no active project to manage"
		return m, nil
	}
	return m.summonGroup(g, "no active project to manage")
}

// summonGroup opens the panel over one group, or says why it cannot. The row's
// directory travels with its name (D20): the popup opens wherever the switcher
// stands, which is not where the project does, and a project is (work
// directory, name).
func (m Model) summonGroup(g core.ProjectGroup, noName string) (tea.Model, tea.Cmd) {
	if g.Name == "" {
		// The summon names its project on the child's command line, so an
		// unnamed group has nothing to hand it.
		m.status = noName
		return m, nil
	}
	return m, core.SummonProjectManagerCmd(
		m.bgCtx(), m.backend, g.Name, g.Path, g.Workdir, groupLabel(g))
}

// muxDownSelected is `d`: the selected project's dashboard windows go away and
// its commands keep running, the dashboard being only a viewer of them. It asks
// nothing first because nothing supervised is lost, and a project whose spec has
// no mux section comes back as the reason on the hint line — the key stays on
// offer for it, since which projects have a dashboard is not something the
// listing says.
func (m Model) muxDownSelected() (tea.Model, tea.Cmd) {
	target, ok := m.downTarget()
	if !ok {
		return m, nil
	}
	return m, core.MuxDownCmd(m.bgCtx(), m.backend, target)
}

// confirmComposeDown is `D`: it puts the question on the hint line instead of
// tearing the project down where it stands. Compose down takes away every
// command of the project, the ones the cursor is not on included, so it is not
// a keystroke to make by accident.
func (m Model) confirmComposeDown() (tea.Model, tea.Cmd) {
	target, ok := m.downTarget()
	if !ok {
		return m, nil
	}
	m.pendingDown = target
	return m, nil
}

// answerComposeDown spends the key the question was waiting for: y tears the
// project down and anything else takes the question back. Either way the key is
// used up rather than passed on — a key that cancelled and moved the cursor in
// one press would leave the user acting on a project they were still answering
// about.
func (m Model) answerComposeDown(key string) (tea.Model, tea.Cmd) {
	target := m.pendingDown
	m.pendingDown = core.DownTarget{}
	if key != "y" {
		m.status = core.ComposeDownCancelled(target.Project)
		return m, nil
	}
	m.status = ""
	return m, core.ComposeDownCmd(m.bgCtx(), m.backend, target)
}

// downTarget names the selected project for a teardown. Both teardowns address
// the project by name, so a group the project listing never claimed a name for
// has nothing to hand them; its directory travels along because a compose file
// names a project only together with the directory it stands in.
func (m *Model) downTarget() (core.DownTarget, bool) {
	g, ok := m.selectedGroup()
	if !ok {
		return core.DownTarget{}, false
	}
	if g.Name == "" {
		m.status = "no project to tear down here"
		return core.DownTarget{}, false
	}
	return core.DownTarget{Project: g.Name, Path: g.Path, WorkDir: g.Workdir}, true
}

// onProjectManagerSummoned reports a summon, the way onProjectSwitched reports
// a switch: a popup that ran needs no word, and where there was no popup to run
// it in the reason takes the hint line (D4).
func (m Model) onProjectManagerSummoned(msg core.ProjectManagerSummonedMsg) Model {
	m.status = ""
	if msg.Err != nil {
		m.status = fmt.Sprintf("manage %s: %v", msg.Name, msg.Err)
	}
	return m
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

// rebuild re-joins the two listings into the switcher's groups, dresses them in
// what the monitors last pushed, and keeps the selection on the project it was
// on across a reload.
func (m Model) rebuild() Model {
	if m.backend != nil {
		m.cwd = m.backend.Cwd()
	}
	var prev string
	if g, ok := m.selectedGroup(); ok {
		prev = g.Key()
	}
	m.groups = switcherGroups(m.projs, m.cmds, m.cwd, m.activeIdentity, m.titles)
	// Before applyBellRead below: what a selection answered for is a bell the
	// cache is still reporting unread (D22), so the suppression has to run over
	// the rows the pushes wrote, not the empty ones the list built.
	applyRuntime(m.groups, m.runtime)
	m.selected = 0
	for i, g := range m.groups {
		if g.Key() == prev {
			m.selected = i
			break
		}
	}
	return m.applyBellRead()
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
//
// cwd and activeIdentity are what the joined groups are marked "you are here"
// with; see activeMark for which of the two answers.
func switcherGroups(
	projs []core.ProjectInfo,
	cmds []core.CommandInfo,
	cwd string,
	activeIdentity string,
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
		g := core.ProjectGroup{
			Name:     p.Name,
			Workdir:  p.Workdir,
			Identity: p.Identity,
			Path:     p.Path,
		}
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
		out[i].Active = activeMark(out[i], cwd, activeIdentity)
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

// activeMark is "you are here" for one group (D3). The mux window the caller
// sits in — or was handed a token for — holds exactly one project, so its
// ownership stamp answers for every group and a group without that stamp is not
// where the user is. Only when no window answered at all does the mark fall
// back to the directory the caller is standing in, the older question, which a
// popup summoned from somewhere else cannot answer.
func activeMark(g core.ProjectGroup, cwd, activeIdentity string) bool {
	if activeIdentity != "" {
		return g.Identity == activeIdentity
	}
	return g.Workdir != "" && g.Workdir == cwd
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

// titleStamp is a command's current title and when the monitor pushed it. The
// monitor serves no title timestamp, so "when the title changed" is observed
// here — but from the stream now, not from a poll: every change arrives as its
// own push, and that arrival is the time (L4). A title a command was already
// wearing when the TUI started dates from the snapshot the stream opens with,
// which puts the commands subscribed together in one bucket until something
// actually retitles — the honest answer, since nothing else is known.
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

// activeGroup is the group the caller's own window belongs to (see activeMark).
// An identity answers for exactly one group, and where the directory fallback
// answers for several the first in list order is taken — the active groups sort
// to the top, so that is the one at the head of the list either way.
func (m Model) activeGroup() (core.ProjectGroup, bool) {
	for _, g := range m.groups {
		if g.Active {
			return g, true
		}
	}
	return core.ProjectGroup{}, false
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

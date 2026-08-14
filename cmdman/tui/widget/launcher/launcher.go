// Package launcher implements the launcher widget: the popup-summoned two-pane
// selector over the merged history / project / running-window list (D7, D28).
//
// It is its own package rather than a branch of the root model because its keys
// are zoned — a bare letter types in the input and acts on a list — so it shares
// no key handling with the docked widgets. Everything it shares with them lives
// in cmdman/tui/internal/core.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// launcherZone is which of the three zones has the keyboard (D28 as amended).
// The input is a zone of its own precisely so bare letters never have to mean
// two things: in the input they type, on a list they act — the empty-input rule
// it replaces silently started projects on the first keystroke of `src`.
type launcherZone int

const (
	zoneInput launcherZone = iota // the initial state
	zoneLeft
	zoneRight
)

// launcherProject is a core.LaunchProject plus the launcher's own per-row state.
type launcherProject struct {
	core.LaunchProject
	enabled  bool
	starting bool
}

// launcherLocation is a core.LaunchLocation whose projects carry launcher state.
type launcherLocation struct {
	core.LaunchLocation
	projects []launcherProject
}

// label is the location's name: repo(branch) for a git work tree, the directory
// basename otherwise (D18).
func (l launcherLocation) label() string {
	if l.RepoName == "" {
		return filepath.Base(l.Dir)
	}
	if l.Branch == "" {
		return l.RepoName
	}
	return l.RepoName + "(" + l.Branch + ")"
}

// marker folds the location's projects into one marker state, so the left pane
// answers "what is going on down there" without opening it: a bring-up in
// flight anywhere shows through, and so does the attention of whatever is
// running (D29 precedence, D14 aggregation).
func (l launcherLocation) marker() launcherMarker {
	var mk launcherMarker
	for _, p := range l.projects {
		if p.starting {
			mk.starting = true
		}
		if !p.Running {
			continue
		}
		mk.running = true
	}
	return mk
}

// newLauncherLocations wraps the backend listing in the launcher's own row
// state. History projects arrive enabled, so the everyday case really is
// "summon, s" (D28).
func newLauncherLocations(locs []core.LaunchLocation) []launcherLocation {
	out := make([]launcherLocation, 0, len(locs))
	for _, l := range locs {
		ll := launcherLocation{
			LaunchLocation: l,
			projects:       make([]launcherProject, 0, len(l.Projects)),
		}
		for _, p := range l.Projects {
			ll.projects = append(
				ll.projects,
				launcherProject{LaunchProject: p, enabled: p.FromHistory},
			)
		}
		out = append(out, ll)
	}
	return out
}

// Model is the launcher widget: the popup-summoned two-pane selector over the
// merged history / project / running-window list (D7, D28). It is the whole
// program when Options.Widget names the launcher, not a view inside another.
type Model struct {
	backend core.Backend

	// ctx is the program-scoped context used for backend calls. A bubbletea
	// Update takes no context of its own, so the one New was given is held here;
	// a zero-value Model built by a test has none and bgCtx covers that.
	ctx context.Context

	width, height int
	altScreen     bool
	// noQuit unbinds the quit gestures, as it does for the docked widgets (V6).
	noQuit bool

	locs   []launcherLocation
	loaded bool

	filter string
	focus  launcherZone

	// home is what the input's "~" and "$HOME" expand to, and what a completion
	// is written back with. It is read once by New; a zero-value Model built by a
	// test simply has no home to expand against, as the view's homeDir() has none
	// when the environment cannot say.
	home string

	// resolveGen sequences the debounced typed-path lookup: a tick that a later
	// keystroke has already superseded carries the older generation and is
	// dropped, so a path typed one character at a time costs one backend call.
	resolveGen int

	leftSel, leftOff   int
	rightSel, rightOff int

	// failedLoc/failedPrj point at the row whose failure is spelled out inline,
	// -1 when none. Errors that prevent landing stay in the launcher rather than
	// dismissing it (D10).
	failedLoc, failedPrj int
	failedMsg            string

	// note is a transient one-liner, cleared on the next keystroke.
	note string

	// spin advances the in-flight marker and ticking says a tick loop is already
	// running, so a second `s` does not start a second one (D29).
	spin    int
	ticking bool

	termFg, termBg color.Color
	fgDark         bool
	weak           color.Color

	quitting bool
}

// New constructs the launcher model from the same options every other TUI mode
// takes. ctx is the program's, and is what the backend calls the model issues
// are made under.
func New(ctx context.Context, opts core.Options) Model {
	return Model{
		ctx:       ctx,
		backend:   opts.Backend,
		altScreen: opts.AltScreen,
		noQuit:    opts.NoQuit,
		home:      homeDir(),
		failedLoc: -1,
		failedPrj: -1,
	}
}

// quit ends the run, unless --no-quit took the gesture away (V6).
func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.noQuit {
		return m, nil
	}
	m.quitting = true
	return m, tea.Quit
}

func (m Model) bgCtx() context.Context {
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// Init implements tea.Model. The listing is taken once at open: git info costs
// an exec per entry (D41), and a branch that changes while the launcher is up is
// acceptable to miss.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		listLaunchTargetsCmd(m.bgCtx(), m.backend),
		tea.RequestForegroundColor,
		tea.RequestBackgroundColor,
	)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.clampLeft(), nil
	case tea.ForegroundColorMsg:
		m.termFg, m.fgDark = msg.Color, msg.IsDark()
		return m.deriveWeak(), nil
	case tea.BackgroundColorMsg:
		m.termBg = msg.Color
		return m.deriveWeak(), nil
	case launchTargetsLoadedMsg:
		return m.onTargetsLoaded(msg)
	case launcherStartedMsg:
		return m.onStarted(msg), nil
	case launcherLandedMsg:
		return m.onLanded(msg)
	case launcherAttachedMsg:
		return m.onAttached(msg)
	case launcherForgotMsg:
		return m.onForgot(msg), nil
	case core.ReloadTickMsg:
		return m.resolveTyped(msg)
	case launcherResolvedMsg:
		return m.onResolved(msg), nil
	case launcherTickMsg:
		return m.tick()
	case tea.MouseWheelMsg:
		return m.wheel(msg), nil
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		return m.clickAt(msg.X, msg.Y), nil
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) onTargetsLoaded(msg launchTargetsLoadedMsg) (tea.Model, tea.Cmd) {
	m.loaded = true
	if msg.err != nil {
		m.note = fmt.Sprintf("list error: %v", msg.err)
		return m, nil
	}
	m.locs = newLauncherLocations(msg.locs)
	// The listing replaces every row, including one a path typed while it was in
	// flight had already resolved to; asking for that row again is cheaper than
	// merging two listings, and costs an idle tick when nothing was typed.
	return m.clampLeft().armResolve()
}

// onStarted promotes a finished bring-up: the spinner stops and the row becomes
// running, or the failure is spelled out where the row is (D10).
func (m Model) onStarted(msg launcherStartedMsg) Model {
	li, pi, ok := m.find(msg.target)
	if !ok {
		return m
	}
	m.locs[li].projects[pi].starting = false
	if msg.err != nil {
		return m.fail(li, pi, msg.err)
	}
	m.locs[li].projects[pi].Running = true
	return m
}

// onLanded reports a launch+focus. A landing that worked leaves nothing to look
// at, so the launcher dismisses (D10) — except for the two outcomes that still
// have something to do: a warning to read (D9) or a terminal to hand over (D8).
func (m Model) onLanded(msg launcherLandedMsg) (tea.Model, tea.Cmd) {
	li, pi, ok := m.find(msg.target)
	if ok {
		m.locs[li].projects[pi].starting = false
	}
	if msg.err != nil {
		if ok {
			return m.fail(li, pi, msg.err), nil
		}
		m.note = msg.err.Error()
		return m, nil
	}
	if ok {
		m.locs[li].projects[pi].Running = true
	}
	switch {
	case msg.outcome.Landed():
		m.quitting = true
		return m, tea.Quit
	case len(msg.outcome.AttachCommand) > 0:
		// Outside the multiplexer the landing is this terminal becoming its
		// client, which only the process holding the terminal can do — so the
		// launcher releases it and runs the attach itself.
		m.note = msg.outcome.Warning
		return m, attachCmd(msg.outcome.AttachCommand)
	default:
		// The project is up and focus moved; the launcher stays only so the
		// warning is read (D9). Its own dismissal keys apply as usual.
		m.note = msg.outcome.Warning
		return m, nil
	}
}

// onAttached closes the launcher after the terminal comes back from the
// multiplexer: the landing is over either way, and a failed handoff is worth
// saying out loud rather than dismissing into.
func (m Model) onAttached(msg launcherAttachedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.note = fmt.Sprintf("attach: %v", msg.err)
		return m, tea.ClearScreen
	}
	m.quitting = true
	return m, tea.Quit
}

func (m Model) onForgot(msg launcherForgotMsg) Model {
	li, pi, ok := m.find(msg.target)
	if !ok {
		return m
	}
	if msg.err != nil {
		return m.fail(li, pi, msg.err)
	}
	m.locs[li].projects = slices.Delete(m.locs[li].projects, pi, pi+1)
	m.failedLoc, m.failedPrj, m.failedMsg = -1, -1, ""
	m.note = "removed from history — " + msg.target.Project
	return m.clampRight()
}

// --- typed paths (D28) ------------------------------------------------------

// armResolve schedules the typed-path lookup after an edit to the query. It is
// debounced rather than fired per keystroke because a path typed one character
// at a time passes through every directory on the way down, and resolving one
// costs the backend a git read per entry (D41). The tick is core's shared
// debounce: the launcher is the whole program when it runs, so no parent model
// forwards a ReloadTickMsg of its own into it.
func (m Model) armResolve() (tea.Model, tea.Cmd) {
	m.resolveGen++
	return m, core.DebounceCmd(m.resolveGen)
}

// resolveTyped asks the backend for the row a typed directory would have listed
// as, once the typing has settled: a path the user types their way to is a
// location like any other (D28). A directory the listing already carries is left
// alone — its row is the richer one.
func (m Model) resolveTyped(msg core.ReloadTickMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.resolveGen {
		return m, nil
	}
	dir, ok := m.typedDir()
	if !ok || m.knownDir(dir) || !dirExists(dir) {
		return m, nil
	}
	return m, resolveLaunchDirCmd(m.bgCtx(), m.backend, dir)
}

// onResolved puts a typed path's row in the left pane. The reply carries the
// directory it was asked for: typing moves on while the backend reads the
// location, and a row the input no longer names is a row with nothing on screen
// to explain it.
func (m Model) onResolved(msg launcherResolvedMsg) Model {
	if dir, ok := m.typedDir(); !ok || dir != msg.dir {
		return m
	}
	if msg.err != nil {
		m.note = fmt.Sprintf("resolve error: %v", msg.err)
		return m
	}
	if m.knownDir(msg.loc.Dir) {
		return m
	}
	loc := msg.loc
	// Out of history whatever the resolve merged into it: the empty query is
	// D28's history list, and a directory the user typed their way to has not
	// joined it. Appended rather than inserted because failedLoc indexes m.locs —
	// a row put anywhere but the end would move a failure off its own row.
	loc.FromHistory = false
	m.locs = append(m.locs, newLauncherLocations([]core.LaunchLocation{loc})...)
	return m.clampLeft()
}

// typedDir is the directory the input names, in the canonical form the listing
// and the history table share (see core.LaunchTarget). It reports false for a
// query that is not a path at all.
func (m Model) typedDir() (string, bool) {
	if !pathShaped(m.filter) {
		return "", false
	}
	abs, err := filepath.Abs(expandHome(m.filter, m.home))
	if err != nil {
		return "", false
	}
	return abs, true
}

func (m Model) knownDir(dir string) bool {
	return slices.ContainsFunc(m.locs, func(l launcherLocation) bool { return l.Dir == dir })
}

// fail puts an error where its row is and moves the cursor onto it: an error the
// user cannot see is an error they cannot act on.
func (m Model) fail(li, pi int, err error) Model {
	m.failedLoc, m.failedPrj, m.failedMsg = li, pi, err.Error()
	if cur, ok := m.currentLoc(); ok && cur == li {
		m.focus, m.rightSel = zoneRight, pi
		return m.clampRight()
	}
	return m
}

// find locates the row an async result belongs to. Results are matched by
// identity rather than by index because the list can be edited (a stale entry
// dropped) while a bring-up is in flight.
func (m Model) find(t core.LaunchTarget) (li, pi int, ok bool) {
	for i := range m.locs {
		if m.locs[i].Dir != t.WorkDir {
			continue
		}
		for j := range m.locs[i].projects {
			if m.locs[i].projects[j].Name == t.Project {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

// --- keys -------------------------------------------------------------------

func (m Model) onKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.note = ""
	before := m.filter
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		// One step at a time: projects → locations → input → clear the query →
		// dismiss. Clearing comes before quitting so a long path typed by mistake
		// costs one key, not the whole popup (D31).
		switch {
		case m.focus == zoneRight:
			m.focus = zoneLeft
		case m.focus == zoneLeft:
			m.focus = zoneInput
		case m.filter != "":
			m.filter = ""
			m = m.clampLeft()
		default:
			return m.quit()
		}
	case "ctrl+u":
		// Readline's erase-line, reachable from anywhere. It returns to the input
		// too: having just emptied the query you are about to retype it.
		m.filter, m.focus = "", zoneInput
		m = m.clampLeft()
	case "tab":
		if m.focus == zoneInput {
			m = m.complete()
		}
	case "enter":
		switch m.focus {
		case zoneInput:
			m.focus = zoneLeft
			m = m.clampLeft()
		case zoneLeft:
			m.focus = zoneRight
			m = m.clampRight()
		case zoneRight:
			m = m.toggle()
		}
	case "down", "ctrl+n":
		m = m.move(1)
	case "up", "ctrl+p":
		m = m.move(-1)
	case "ctrl+d":
		return m.drop()
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 && m.focus == zoneInput {
			m.filter = string(r[:len(r)-1])
			m = m.clampLeft()
		}
	default:
		return m.text(msg.Text)
	}
	if m.filter == before {
		return m, nil
	}
	return m.armResolve()
}

// text applies a printable key. This is what the three zones buy (D28 amended):
// in the input every key is text, on a list every key is a command, and no key
// has to guess which was meant.
func (m Model) text(t string) (tea.Model, tea.Cmd) {
	if t == "" {
		return m, nil
	}
	if m.focus == zoneInput {
		m.filter += t
		m.failedLoc, m.failedPrj, m.failedMsg = -1, -1, ""
		return m.clampLeft().armResolve()
	}
	switch t {
	case "/":
		m.focus = zoneInput
	case " ":
		if m.focus == zoneRight {
			return m.toggle(), nil
		}
	case "j":
		return m.move(1), nil
	case "k":
		return m.move(-1), nil
	case "s":
		return m.startEnabled()
	case "S":
		return m.launchSelected()
	case "q":
		return m.quit()
	}
	return m, nil
}

// complete extends the input toward the one path it could still be: the common
// prefix of the locations it matches and, while a path is being typed, of the
// directories on disk that extend it (D28). Tab in a path picker should move you
// down the tree, never widen the search.
func (m Model) complete() Model {
	exp := expandHome(m.filter, m.home)
	cands := m.completions(exp)
	if len(cands) == 0 {
		return m
	}
	p := commonPrefix(cands)
	if pathShaped(m.filter) && endsAtDir(p, cands) {
		// The separator is what lets the next tab reach inside; without it a path
		// completed onto a directory name needs a keystroke the user did not have
		// to guess at.
		p += "/"
	}
	if len(p) <= len(exp) {
		m.note = "no common prefix past the current input"
		return m
	}
	m.filter = m.respell(p)
	return m.clampLeft()
}

// completions is what tab may complete to: the paths of the locations the input
// matches, plus the directories on disk under a path being typed. A matched
// location that does not extend that path is dropped — it can only drag the
// common prefix back behind the cursor, which is the widening tab must never do,
// and a relative path has no absolute location to share a prefix with at all.
func (m Model) completions(exp string) []string {
	shaped := pathShaped(m.filter)
	out := make([]string, 0, len(m.locs))
	for _, i := range m.matched() {
		if shaped && !strings.HasPrefix(m.locs[i].Dir, exp) {
			continue
		}
		out = append(out, m.locs[i].Dir)
	}
	if shaped {
		out = append(out, fsDirCandidates(exp)...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// endsAtDir reports that the completed prefix is a directory the input can only
// go inside: every candidate is that directory or something under it. A sibling
// sharing the same letters (proj and proj-2) is not, and a separator there would
// cut the sibling out of the search.
func endsAtDir(p string, cands []string) bool {
	if p == "" || strings.HasSuffix(p, "/") || !dirExists(p) {
		return false
	}
	for _, c := range cands {
		if c != p && !strings.HasPrefix(c, p+"/") {
			return false
		}
	}
	return true
}

// respell writes a completed path back the way the input spells home: a query
// typed as ~/src or $HOME/src must not turn into /home/u/src under the cursor.
func (m Model) respell(p string) string {
	tok, ok := homeToken(m.filter)
	if !ok {
		return p
	}
	a := abbrevHome(p, m.home)
	if a == p {
		return p
	}
	return tok + strings.TrimPrefix(a, "~")
}

// --- selection --------------------------------------------------------------

// matched lists the locations the input selects: history only while the input is
// empty (D28's opening state), every matching location once the user types.
func (m Model) matched() []int {
	out := make([]int, 0, len(m.locs))
	path := trimTrailingSep(expandHome(m.filter, m.home))
	for i, l := range m.locs {
		if m.filter == "" {
			if l.FromHistory {
				out = append(out, i)
			}
			continue
		}
		if _, ok := matchLaunchLocation(m.filter, path, l); ok {
			out = append(out, i)
		}
	}
	return out
}

// currentLoc is the location under the left cursor.
func (m Model) currentLoc() (int, bool) {
	f := m.matched()
	if m.leftSel < 0 || m.leftSel >= len(f) {
		return 0, false
	}
	return f[m.leftSel], true
}

// currentPrj is the project under the right cursor.
func (m Model) currentPrj() (li, pi int, ok bool) {
	li, ok = m.currentLoc()
	if !ok || m.rightSel < 0 || m.rightSel >= len(m.locs[li].projects) {
		return 0, 0, false
	}
	return li, m.rightSel, true
}

// move drives the focused list. Arrows and ctrl+p/n work from the input zone too
// and steer the left list there, the fzf reflex: you type, you arrow down to the
// row you meant, without leaving the query.
func (m Model) move(d int) Model {
	if m.focus == zoneRight {
		li, ok := m.currentLoc()
		if !ok || len(m.locs[li].projects) == 0 {
			return m
		}
		n := len(m.locs[li].projects)
		m.rightSel = (m.rightSel + d + n) % n
		return m.clampRight()
	}
	n := len(m.matched())
	if n == 0 {
		return m
	}
	m.leftSel = (m.leftSel + d + n) % n
	// The right pane follows the left cursor live.
	m.rightSel, m.rightOff = 0, 0
	return m.clampLeft()
}

func (m Model) clampLeft() Model {
	f := m.matched()
	if m.leftSel >= len(f) {
		m.leftSel = max(len(f)-1, 0)
	}
	cost := make([]int, len(f))
	for i := range cost {
		cost[i] = 1
	}
	m.leftOff = scrollTo(m.leftSel, m.leftOff, m.budget(), cost)
	return m.clampRight()
}

func (m Model) clampRight() Model {
	li, ok := m.currentLoc()
	if !ok {
		m.rightSel, m.rightOff = 0, 0
		return m
	}
	ps := m.locs[li].projects
	if m.rightSel >= len(ps) {
		m.rightSel = max(len(ps)-1, 0)
	}
	m.rightOff = scrollTo(m.rightSel, m.rightOff, m.budget(), m.rightCost(li))
	return m
}

// rightCost is each right-pane row's line count: the row whose failure is spelled
// out spends a second line on the message under it.
func (m Model) rightCost(li int) []int {
	cost := make([]int, len(m.locs[li].projects))
	for i := range cost {
		cost[i] = 1
		if li == m.failedLoc && i == m.failedPrj {
			cost[i] = 2
		}
	}
	return cost
}

// --- actions ----------------------------------------------------------------

func (m Model) toggle() Model {
	li, pi, ok := m.currentPrj()
	if !ok {
		return m
	}
	m.locs[li].projects[pi].enabled = !m.locs[li].projects[pi].enabled
	return m
}

// startEnabled brings up every enabled project at the location and leaves it
// alone — D4's background start, D28's `s`. It opens no progress view (D29): a
// bulk start must not re-block the very thing `s` exists to avoid, so the
// feedback is the rows' marker turning into a spinner while the launcher stays
// entirely usable. Projects that are already running are skipped (D31): `s` is
// "get the rest warming up", not "restart what works".
//
// A project that cannot be brought up at all surfaces on its own row (D10) and
// the rest of the batch still starts: dropping the whole batch on the first
// problem would leave the rows already marked starting spinning against no
// bring-up at all, since the marks and the commands are made in one pass.
func (m Model) startEnabled() (tea.Model, tea.Cmd) {
	li, ok := m.currentLoc()
	if !ok {
		return m, nil
	}
	var (
		cmds    []tea.Cmd
		names   []string
		enabled int
		problem = -1
	)
	for i, p := range m.locs[li].projects {
		if !p.enabled {
			continue
		}
		enabled++
		if p.Problem != "" {
			// The first one: the inline slot holds one row, and the earliest is
			// the one the cursor lands on.
			if problem < 0 {
				problem = i
			}
			continue
		}
		if p.Running || p.starting {
			continue
		}
		m.locs[li].projects[i].starting = true
		names = append(names, p.Name)
		cmds = append(cmds, startProjectCmd(m.bgCtx(), m.backend, m.target(li, i)))
	}
	if enabled == 0 {
		m.note = "nothing enabled here — space toggles a project"
		return m, nil
	}
	if problem >= 0 {
		m = m.fail(li, problem, errors.New(m.locs[li].projects[problem].Problem))
	}
	if len(names) == 0 {
		if problem < 0 {
			m.note = "already up here"
		}
		return m, nil
	}
	m.note = "starting " + strings.Join(names, ", ")
	mm, tick := m.startTicking()
	return mm, tea.Batch(append(cmds, tick)...)
}

// launchSelected is launch + focus (D4's `S`): bring the project up and land in
// its window. A compose file that no longer resolves cannot land at all, so the
// error stays in the launcher and offers removal instead of failing cryptically
// (D10).
func (m Model) launchSelected() (tea.Model, tea.Cmd) {
	li, ok := m.currentLoc()
	if !ok {
		return m, nil
	}
	pi := m.pickProject(li)
	if pi < 0 {
		m.note = "no compose project here"
		return m, nil
	}
	p := m.locs[li].projects[pi]
	if p.Problem != "" {
		return m.fail(li, pi, errors.New(p.Problem)), nil
	}
	if !p.starting {
		m.locs[li].projects[pi].starting = true
	}
	m.note = "launching " + p.Name
	mm, tick := m.startTicking()
	return mm, tea.Batch(launchProjectCmd(m.bgCtx(), m.backend, m.target(li, pi)), tick)
}

// pickProject is what `S` acts on: the project under the right cursor when that
// pane has focus, otherwise the location's first enabled one.
func (m Model) pickProject(li int) int {
	ps := m.locs[li].projects
	if len(ps) == 0 {
		return -1
	}
	if m.focus == zoneRight && m.rightSel < len(ps) {
		return m.rightSel
	}
	for i, p := range ps {
		if p.enabled {
			return i
		}
	}
	return 0
}

// drop removes a stale history entry, the offer D10 makes when resolution fails.
// History is never dropped silently, only on request, and only for the row that
// actually cannot be resolved.
func (m Model) drop() (tea.Model, tea.Cmd) {
	li, pi, ok := m.currentPrj()
	if !ok || !m.locs[li].projects[pi].Missing {
		return m, nil
	}
	return m, forgetTargetCmd(m.bgCtx(), m.backend, m.target(li, pi))
}

func (m Model) target(li, pi int) core.LaunchTarget {
	return core.LaunchTarget{
		WorkDir: m.locs[li].Dir,
		Project: m.locs[li].projects[pi].Name,
		File:    m.locs[li].projects[pi].File,
	}
}

// --- spinner ----------------------------------------------------------------

// launcherSpinInterval is the in-flight marker's cadence.
const launcherSpinInterval = 120 * time.Millisecond

type launcherTickMsg struct{}

func launcherTickCmd() tea.Cmd {
	return tea.Tick(launcherSpinInterval, func(time.Time) tea.Msg { return launcherTickMsg{} })
}

// startTicking arms the animation loop, unless one is already in flight — a
// second `s` elsewhere must not leave two loops ticking against each other.
func (m Model) startTicking() (Model, tea.Cmd) {
	if m.ticking {
		return m, nil
	}
	m.ticking = true
	return m, launcherTickCmd()
}

// tick advances the spinner. It re-arms only while something is still starting,
// so an idle launcher costs nothing.
func (m Model) tick() (tea.Model, tea.Cmd) {
	m.spin++
	if !m.anyStarting() {
		m.ticking = false
		return m, nil
	}
	return m, launcherTickCmd()
}

func (m Model) anyStarting() bool {
	for i := range m.locs {
		for _, p := range m.locs[i].projects {
			if p.starting {
				return true
			}
		}
	}
	return false
}

// --- matching (D18) ---------------------------------------------------------

// matchLaunchLocation reports the first field the filter matches, so the list and
// a reviewer can both see *why* a row survived. Branch, repo uri and path are
// D18's location fields; project names are kept as a last resort because the
// projects moved to the right pane and typing one should still find its home.
//
// path is the filter with its home spelling expanded (see expandHome). Only the
// path field takes it: the row stores its directory in full, so a query typed
// the way the pane displays it has to be read back the way the row holds it,
// while a branch or a repo named "~" would be exactly that string.
func matchLaunchLocation(filter, path string, l launcherLocation) (string, bool) {
	for _, f := range []struct{ name, value, needle string }{
		{"branch", l.Branch, filter},
		{"repo", l.RepoURI, filter},
		{"repo", l.RepoName, filter},
		{"path", l.Dir, path},
	} {
		if f.value != "" && core.MatchesFilter(f.needle, f.value) {
			return f.name, true
		}
	}
	for _, p := range l.projects {
		if core.MatchesFilter(filter, p.Name) {
			return "project", true
		}
	}
	return "", false
}

// commonPrefix is what tab completes the input to.
func commonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, out) {
			out = out[:len(out)-1]
			if out == "" {
				return ""
			}
		}
	}
	return out
}

// --- scrolling --------------------------------------------------------------

// rowWindow is how many rows starting at off fit the budget, counted in lines
// because a row carrying an error message is two lines tall.
func rowWindow(cost []int, off, budget int) int {
	used, k := 0, off
	for ; k < len(cost); k++ {
		if used+cost[k] > budget {
			break
		}
		used += cost[k]
	}
	return k - off
}

// scrollTo brings sel into view and keeps the window full: it walks up until the
// cursor is visible, then back down while rows remain to pull in — a narrowing
// list would otherwise strand the window part-way down, hiding rows above with
// blank space below.
func scrollTo(sel, off, budget int, cost []int) int {
	n := len(cost)
	if n == 0 {
		return 0
	}
	off = min(max(off, 0), n-1, max(sel, 0))
	for range n {
		if sel < off+rowWindow(cost, off, budget) || off >= n-1 {
			break
		}
		off++
	}
	for off > 0 && sel < off-1+rowWindow(cost, off-1, budget) {
		off--
	}
	return off
}

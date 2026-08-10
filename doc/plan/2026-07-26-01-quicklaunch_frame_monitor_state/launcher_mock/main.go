// Command launcher_mock is a disposable usability mock for the *full-sized*
// form factor of the selector (doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state)
// — the popup summoned by a mux key binding (D3), as opposed to the docked
// side-bar and bottom-row forms mocked in ../frame_mock. It shares that mock's
// visual vocabulary (marker slot, colored dot / 🔔 / hollow ○, selection
// block, terminal-derived weak color); the helpers are copied rather than
// imported because both are throwaway package mains.
//
// The launcher asks two questions in order, so it has two panes (D28):
//
//   - Left, the *location*: a fuzzy-found list over the input line, with tab
//     completing toward the matching locations' common path prefix. An empty
//     input is history mode — recency-ordered locations you have launched
//     before (D7). Rows render git-aware, repo(branch) or basename (D18).
//   - Right, the *project* at the location under the left cursor: everything
//     known from history plus the compose files sitting in that directory,
//     each toggled on or off. History projects arrive pre-enabled, so the
//     everyday case really is "summon, s".
//
// Focus moves through three zones (D28 amended), and Enter is what advances
// it: input → left list → right list. The input is a zone of its own so that
// bare letters never have to mean two things — in the input every key types,
// on a list every key is a command. The first spelling of this rule ("s acts
// while the input is empty") was tried here and failed: history mode is the
// opening state, so the first keystroke of `src` or `staging` silently
// started projects.
//
// `s` starts every enabled project at the location and leaves; `S` jumps into
// the path and attaches to the mux project (D28 revises D4's launcher
// spelling: Enter no longer launches, but D4's two endings — start-only and
// launch+focus — stand). Both failure experiences survive from the flat mock:
// a compose file that no longer resolves cannot land, so `S` surfaces the
// error inline and offers removal (D10); a project with no mux section lands
// anyway on a synthesized shell window, with a warning (D9).
//
// `s` opens no progress screen (D29). A bulk start would be re-blocked by the
// very view meant to report it, so bring-up shows as a spinner in the marker
// slot — on the project rows and, folded through the location, on the left
// pane — and the launcher stays fully usable while it turns: keep typing,
// keep navigating, toggle more, start somewhere else. Each fake bring-up
// finishes on its own staggered delay, so a bulk start visibly completes out
// of order. The animation loop runs only while something is in flight.
//
// The launcher fills its window edge to edge (D27): `tmux display-popup`
// already creates the small centered window and draws its border, and hands
// the program that window as its whole terminal. Run it in a real popup to
// judge the proportions:
//
//	tmux display-popup -E -w 100 -h 24 \
//	  'go run ./doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state/launcher_mock'
//
// Run (any terminal; resize it small to approximate a popup):
//
//	go run ./doc/plan/2026-07-26-01-quicklaunch_frame_monitor_state/launcher_mock
//
// Keys, by zone. Input: type to filter · tab completes the path · enter moves
// to the locations. Lists: j/k or ↑↓ move · enter advances (and toggles on
// the right) · space toggles · s starts the enabled projects · S jumps in and
// attaches · q quits. Anywhere: ↑↓/ctrl+p/n drive the list even from the
// input · / returns to the input · ctrl+u erases it · ctrl+d drops a stale
// entry · esc steps back one zone, then clears the query, then quits ·
// ctrl+c quits.
// Mouse: click the input line to edit it, a location to select it, a project
// to toggle it; the wheel scrolls whichever pane it is over.
package main

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path"
	"slices"
	"strings"
	"time"

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

// --- fake data ---

// proj is one compose project at a location: what the right pane toggles.
type proj struct {
	name     string // compose project name
	file     string // the compose file it comes from
	fromHist bool   // known from history, so it starts enabled (D28)
	enabled  bool
	running  bool
	status   string // waiting | working | done | "" (never reported)
	bell     bool
	noMux    bool // no mux section: lands on a synthesized shell (D9)
	missing  bool // the compose file no longer resolves (D10)

	// readyAt is when this fake bring-up finishes; non-zero means it is in
	// flight, which is what the spinner marker shows (D29).
	readyAt time.Time
}

func (p proj) starting() bool { return !p.readyAt.IsZero() }

// location is a target directory: what the left pane selects.
type location struct {
	dir      string
	repoURI  string // "" when the workdir is not a git repo
	branch   string
	recency  int  // history recency; 0 for a directory never launched from
	fromHist bool // shows up in history mode
	projects []proj
}

func fakeLocations() []location {
	return []location{
		// Two locations carry several local compose files, the case that made
		// a flat `path (project)` list ambiguous and D28 necessary.
		{
			dir:      "/home/watage/gitrepo/github.com/ngicks/cmdman",
			repoURI:  "github.com/ngicks/cmdman",
			branch:   "main",
			recency:  100,
			fromHist: true,
			projects: []proj{
				{
					name:     "devenv",
					file:     "devenv.yaml",
					fromHist: true,
					running:  true,
					status:   "working",
				},
				{name: "test", file: "test.yaml"},
			},
		},
		{
			dir: "/home/watage/src/webapp", repoURI: "github.com/acme/webapp",
			branch: "feat/auth", recency: 98, fromHist: true,
			projects: []proj{
				{
					name: "webapp", file: "compose.yaml", fromHist: true,
					running: true, status: "waiting", bell: true,
				},
			},
		},
		{
			dir: "/home/watage/wt/cmdman-frame", repoURI: "github.com/ngicks/cmdman",
			branch: "feat/frame", recency: 95, fromHist: true,
			projects: []proj{
				{name: "devenv", file: "devenv.yaml", fromHist: true, running: true},
			},
		},
		{
			dir: "/srv/prod/edge", recency: 88, fromHist: true,
			projects: []proj{
				{name: "edge", file: "compose.yaml", fromHist: true, running: true, status: "done"},
				{name: "canary", file: "canary.yaml"},
			},
		},
		{
			dir: "/home/watage/src/infra", repoURI: "github.com/acme/infra",
			branch: "main", recency: 82, fromHist: true,
			projects: []proj{
				{name: "tf", file: "devenv.yaml", fromHist: true},
				{name: "staging", file: "staging.yaml"},
				{name: "prod", file: "prod.yaml"},
			},
		},
		{
			dir: "/opt/monorepo/gateway", repoURI: "git.corp/acme/monorepo",
			branch: "release/2.4", recency: 76, fromHist: true,
			projects: []proj{{name: "authgw", file: "compose.yaml", fromHist: true}},
		},
		{
			dir: "/home/watage/old/demo-stack", recency: 70, fromHist: true,
			projects: []proj{
				{name: "demo", file: "compose.devenv.yml", fromHist: true, missing: true},
			},
		},
		{
			dir: "/home/watage/src/scripts", repoURI: "github.com/acme/scripts",
			branch: "main", recency: 64, fromHist: true,
			projects: []proj{{name: "scripts", file: "compose.yaml", fromHist: true, noMux: true}},
		},
		{
			dir: "/home/watage/src/dashboard", repoURI: "github.com/acme/dashboard",
			branch: "main", recency: 58, fromHist: true,
			projects: []proj{{name: "web", file: "compose.yaml", fromHist: true}},
		},
		// Never launched from: discovered on disk, so they appear only once
		// the input narrows to them.
		{
			dir:     "/home/watage/gitrepo/github.com/ngicks/go-common",
			repoURI: "github.com/ngicks/go-common", branch: "main",
			projects: []proj{{name: "devenv", file: "devenv.yaml"}},
		},
		{
			dir:      "/home/watage/src/blog",
			projects: []proj{{name: "blog", file: "compose.yaml"}},
		},
		// No compose file at all: the right pane has to say so.
		{dir: "/home/watage/src/notes"},
	}
}

// --- matching (D18) ---

// matchesFilter is pkg/cmdman/tui/filter.go's matcher, copied so the mock
// behaves exactly like the shipped TUI: case-insensitive, substring first,
// otherwise every rune of needle in order.
func matchesFilter(needle, haystack string) bool {
	if needle == "" {
		return true
	}
	needle = strings.ToLower(needle)
	haystack = strings.ToLower(haystack)
	if strings.Contains(haystack, needle) {
		return true
	}
	ni := 0
	nr := []rune(needle)
	for _, hc := range haystack {
		if hc == nr[ni] {
			ni++
			if ni == len(nr) {
				return true
			}
		}
	}
	return false
}

// matchLocation reports the first field the filter matches, so the list and a
// reviewer can both see *why* a row survived. Branch, repo and path are D18's
// location fields; project names are kept as a last resort because the
// projects moved to the right pane and typing one should still find its home.
func matchLocation(filter string, l location) (string, bool) {
	for _, f := range []struct{ name, value string }{
		{"branch", l.branch},
		{"repo", l.repoURI},
		{"path", l.dir},
	} {
		if f.value != "" && matchesFilter(filter, f.value) {
			return f.name, true
		}
	}
	for _, p := range l.projects {
		if matchesFilter(filter, p.name) {
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

// --- display (D18) ---

// label is the location's `path`: repo(branch) for a git workdir, the
// directory basename otherwise.
func (l location) label() string {
	if l.repoURI == "" {
		return path.Base(l.dir)
	}
	return path.Base(l.repoURI) + "(" + l.branch + ")"
}

// attention folds the location's projects into one marker state, so the left
// pane still answers "which of these wants me" (D21/D23/D24) — and, while a
// bring-up is in flight there, shows the same progress marker its rows do
// (D29).
func (l location) attention() (status string, bell, running, starting bool) {
	status = "unknown"
	for _, p := range l.projects {
		if p.starting() {
			starting = true
		}
		if !p.running {
			continue
		}
		running = true
		if p.bell {
			bell = true
		}
		switch p.status {
		case "waiting":
			status = "waiting"
		case "working":
			if status != "waiting" {
				status = "working"
			}
		case "done":
			if status == "unknown" {
				status = "done"
			}
		}
	}
	return status, bell, running, starting
}

const home = "/home/watage"

// shortPath abbreviates the home prefix and, when still too long, keeps the
// tail — the end of a path is what distinguishes it.
func shortPath(p string, w int) string {
	if after, ok := strings.CutPrefix(p, home); ok {
		p = "~" + after
	}
	if lipgloss.Width(p) <= w {
		return p
	}
	r := []rune(p)
	if w <= 1 {
		return "…"
	}
	return "…" + string(r[len(r)-w+1:])
}

// --- model ---

// focusZone is which of the three zones has the keyboard (D28 amended). The
// input is a zone of its own precisely so that bare letters never have to
// mean two things: in the input they type, on a list they act.
type focusZone int

const (
	focusInput focusZone = iota // the initial state
	focusLeft
	focusRight
)

type mode int

const (
	modeList mode = iota
	modeLanded
)

type model struct {
	width, height int
	locs          []location
	filter        string
	focus         focusZone

	leftSel, leftOff   int
	rightSel, rightOff int

	mode      mode
	landedLoc int
	landedPrj int
	failedLoc int // location whose project failed to resolve; -1 when none
	failedPrj int
	note      string // transient one-liner, cleared on the next keystroke

	// D29's progress marker: spin advances the spinner, ticking says a tick
	// loop is already in flight so a second `s` does not start a second one.
	spin    int
	ticking bool

	termFg, termBg color.Color
	fgDark         bool
	weak           color.Color
}

func newModel() model {
	locs := fakeLocations()
	// Recency order is settled once: D7 wants positions stable so few-letter
	// muscle memory survives state changes.
	slices.SortStableFunc(locs, func(a, b location) int { return b.recency - a.recency })
	for i := range locs {
		for j := range locs[i].projects {
			// History projects arrive enabled, so the common case is one key.
			locs[i].projects[j].enabled = locs[i].projects[j].fromHist
		}
	}
	return model{locs: locs, failedLoc: -1, failedPrj: -1}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tea.RequestForegroundColor, tea.RequestBackgroundColor)
}

// matched lists the locations the input selects: history only while the input
// is empty (D28), every matching location once the user types.
func (m model) matched() []int {
	out := make([]int, 0, len(m.locs))
	for i, l := range m.locs {
		if m.filter == "" {
			if l.fromHist {
				out = append(out, i)
			}
			continue
		}
		if _, ok := matchLocation(m.filter, l); ok {
			out = append(out, i)
		}
	}
	return out
}

// currentLoc is the location under the left cursor.
func (m model) currentLoc() (int, bool) {
	f := m.matched()
	if m.leftSel < 0 || m.leftSel >= len(f) {
		return 0, false
	}
	return f[m.leftSel], true
}

// currentPrj is the project under the right cursor.
func (m model) currentPrj() (li, pi int, ok bool) {
	li, ok = m.currentLoc()
	if !ok || m.rightSel >= len(m.locs[li].projects) {
		return 0, 0, false
	}
	return li, m.rightSel, true
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	case tickMsg:
		return m.tick(time.Time(msg))
	case tea.MouseWheelMsg:
		return m.wheel(msg), nil
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		return m.clickAt(msg.X, msg.Y), nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeList {
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc", "enter":
			m.mode = modeList
		}
		return m, nil
	}
	m.note = ""
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		// One step at a time: projects → locations → input → clear the query
		// → dismiss. Clearing comes before quitting so a long path typed by
		// mistake costs one key, not the whole popup.
		switch {
		case m.focus == focusRight:
			m.focus = focusLeft
		case m.focus == focusLeft:
			m.focus = focusInput
		case m.filter != "":
			m.filter = ""
			m = m.clampLeft()
		default:
			return m, tea.Quit
		}
	case "ctrl+u":
		// Readline's erase-line, reachable from anywhere. It returns to the
		// input too: having just emptied the query you are about to retype it.
		m.filter, m.focus = "", focusInput
		m = m.clampLeft()
	case "tab":
		if m.focus == focusInput {
			m = m.complete()
		}
	case "enter":
		switch m.focus {
		case focusInput:
			m.focus = focusLeft
			m = m.clampLeft()
		case focusLeft:
			m.focus = focusRight
			m = m.clampRight()
		case focusRight:
			m = m.toggle()
		}
	case "down", "ctrl+n":
		m = m.move(1)
	case "up", "ctrl+p":
		m = m.move(-1)
	case "ctrl+d":
		m = m.drop()
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 && m.focus == focusInput {
			m.filter = string(r[:len(r)-1])
			m = m.clampLeft()
		}
	default:
		return m.text(msg.Text)
	}
	return m, nil
}

// text applies a printable key. This is what the three zones buy (D28
// amended): in the input every key is text, on a list every key is a command,
// and no key has to guess which was meant.
func (m model) text(t string) (tea.Model, tea.Cmd) {
	if t == "" {
		return m, nil
	}
	if m.focus == focusInput {
		m.filter += t
		m.failedLoc, m.failedPrj = -1, -1
		return m.clampLeft(), nil
	}
	switch t {
	case "/":
		m.focus = focusInput
	case " ":
		if m.focus == focusRight {
			return m.toggle(), nil
		}
	case "j":
		return m.move(1), nil
	case "k":
		return m.move(-1), nil
	case "s":
		return m.startEnabled()
	case "S":
		return m.jump()
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// complete extends the input to the common path prefix of what it matches —
// tab in a path picker should move you down the tree, never widen the search.
func (m model) complete() model {
	f := m.matched()
	if len(f) == 0 {
		return m
	}
	dirs := make([]string, 0, len(f))
	for _, i := range f {
		dirs = append(dirs, m.locs[i].dir)
	}
	p := commonPrefix(dirs)
	if len(p) <= len(m.filter) {
		m.note = "no common prefix past the current input"
		return m
	}
	m.filter = p
	return m.clampLeft()
}

// move drives the focused list. Arrows and ctrl+p/n work from the input zone
// too and steer the left list there, the fzf reflex: you type, you arrow down
// to the row you meant, without leaving the query.
func (m model) move(d int) model {
	if m.focus == focusRight {
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

func (m model) clampLeft() model {
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

func (m model) clampRight() model {
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

// rightCost is each right-pane row's line count: a project whose file no
// longer resolves spends a second line on the error under it.
func (m model) rightCost(li int) []int {
	cost := make([]int, len(m.locs[li].projects))
	for i := range cost {
		cost[i] = 1
		if li == m.failedLoc && i == m.failedPrj {
			cost[i] = 2
		}
	}
	return cost
}

// --- actions ---

func (m model) toggle() model {
	li, pi, ok := m.currentPrj()
	if !ok {
		return m
	}
	m.locs[li].projects[pi].enabled = !m.locs[li].projects[pi].enabled
	return m
}

// startEnabled brings up every enabled project at the location and leaves,
// without switching to it — D4's background start, D28's `s`.
// It opens no progress view (D29): a bulk start must not re-block the very
// thing `s` exists to avoid, so the feedback is the rows' marker turning into
// a spinner while the launcher stays entirely usable.
func (m model) startEnabled() (model, tea.Cmd) {
	li, ok := m.currentLoc()
	if !ok {
		return m, nil
	}
	var names []string
	enabled := 0
	now := time.Now()
	for i, p := range m.locs[li].projects {
		if !p.enabled {
			continue
		}
		enabled++
		if p.missing {
			m.failedLoc, m.failedPrj = li, i
			m.focus, m.rightSel = focusRight, i
			mm := m.clampRight()
			return mm, nil
		}
		if p.running || p.starting() {
			continue
		}
		m.locs[li].projects[i].readyAt = now.Add(startDelays[len(names)%len(startDelays)])
		names = append(names, p.name)
	}
	switch {
	case enabled == 0:
		m.note = "nothing enabled here — space toggles a project"
		return m, nil
	case len(names) == 0:
		m.note = "already up here"
		return m, nil
	}
	m.note = "starting " + strings.Join(names, ", ")
	return m.startTicking()
}

// jump is launch + focus: land in the project immediately (D4/D10). A compose
// file that no longer resolves cannot land at all, so the error stays in the
// launcher and offers removal instead of failing cryptically (D10). Landing
// is the focus switch into the mux window, not a progress view — so a cold
// project keeps its spinner while the landed screen is already up (D29).
func (m model) jump() (model, tea.Cmd) {
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
	if p.missing {
		m.failedLoc, m.failedPrj = li, pi
		m.focus, m.rightSel = focusRight, pi
		mm := m.clampRight()
		return mm, nil
	}
	m.landedLoc, m.landedPrj, m.mode = li, pi, modeLanded
	if p.running || p.starting() {
		return m, nil
	}
	m.locs[li].projects[pi].readyAt = time.Now().Add(startDelays[0])
	return m.startTicking()
}

// startDelays fake the bring-up times. They are deliberately not monotonic in
// list order, so a bulk start is seen finishing out of order.
var startDelays = []time.Duration{
	1300 * time.Millisecond,
	700 * time.Millisecond,
	1900 * time.Millisecond,
	1000 * time.Millisecond,
	1600 * time.Millisecond,
}

const spinInterval = 120 * time.Millisecond

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(spinInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// startTicking arms the animation loop, unless one is already in flight — a
// second `s` elsewhere must not leave two loops ticking against each other.
func (m model) startTicking() (model, tea.Cmd) {
	if m.ticking {
		return m, nil
	}
	m.ticking = true
	return m, tickCmd()
}

// tick advances the spinner and promotes the bring-ups that have finished. It
// re-arms only while something is still starting, so an idle launcher costs
// nothing.
func (m model) tick(now time.Time) (model, tea.Cmd) {
	m.spin++
	busy := false
	for i := range m.locs {
		for j := range m.locs[i].projects {
			p := &m.locs[i].projects[j]
			switch {
			case !p.starting():
			case now.Before(p.readyAt):
				busy = true
			default:
				p.readyAt = time.Time{}
				p.running = true
			}
		}
	}
	if !busy {
		m.ticking = false
		return m, nil
	}
	return m, tickCmd()
}

// pickProject is what `S` attaches to: the project under the right cursor
// when that pane has focus, otherwise the location's first enabled one.
func (m model) pickProject(li int) int {
	ps := m.locs[li].projects
	if len(ps) == 0 {
		return -1
	}
	if m.focus == focusRight && m.rightSel < len(ps) {
		return m.rightSel
	}
	for i, p := range ps {
		if p.enabled {
			return i
		}
	}
	return 0
}

// drop removes a stale history project, the offer D10 makes when resolution
// fails. History is never dropped silently, only on request.
func (m model) drop() model {
	li, pi, ok := m.currentPrj()
	if !ok || !m.locs[li].projects[pi].missing {
		return m
	}
	name := m.locs[li].projects[pi].name
	m.locs[li].projects = slices.Delete(m.locs[li].projects, pi, pi+1)
	m.failedLoc, m.failedPrj = -1, -1
	m.note = "removed from history — " + name
	return m.clampRight()
}

// --- scrolling ---

// window is how many rows starting at off fit the budget, counted in lines
// because a row carrying an error message is two lines tall.
func window(cost []int, off, budget int) int {
	used, k := 0, off
	for ; k < len(cost); k++ {
		if used+cost[k] > budget {
			break
		}
		used += cost[k]
	}
	return k - off
}

// scrollTo brings sel into view and keeps the window full: it walks up until
// the cursor is visible, then back down while rows remain to pull in — a
// narrowing list would otherwise strand the window part-way down, hiding rows
// above with blank space below.
func scrollTo(sel, off, budget int, cost []int) int {
	n := len(cost)
	if n == 0 {
		return 0
	}
	off = min(max(off, 0), n-1, max(sel, 0))
	for range n {
		if sel < off+window(cost, off, budget) || off >= n-1 {
			break
		}
		off++
	}
	for off > 0 && sel < off-1+window(cost, off-1, budget) {
		off--
	}
	return off
}

// --- terminal-derived weak color (D26, same derivation as frame_mock) ---

const weakRatio = 0.55

func (m model) deriveWeak() model {
	if m.termFg == nil {
		return m
	}
	bg := m.termBg
	if bg == nil {
		bg = color.Black
		if m.fgDark {
			bg = color.White
		}
	}
	m.weak = blend(m.termFg, bg, weakRatio)
	return m
}

func (m model) weakStyle() lipgloss.Style {
	if m.weak == nil {
		return styleDim
	}
	return lipgloss.NewStyle().Foreground(m.weak)
}

func blend(a, b color.Color, t float64) color.RGBA {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		return uint8(min(math.Round((float64(x)*(1-t)+float64(y)*t)/257), 255))
	}
	return color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 0xff}
}

// --- view ---

// markerSlot is the marker's cell budget — the widest marker (🔔) plus a
// gap — and it is blank where nothing is running. Its width comes off the
// glyph rather than being written down, so a terminal that measures the bell
// differently moves the whole column instead of tearing it.
var markerSlot = glyphWidth(glyphBell) + 1

const (
	// The window is the whole terminal (D27): input line, blank, pane
	// headers, the two panes, a blank, then the hints.
	headLines     = 2
	paneHeadLines = 1
	gapLines      = 1
	paneTop       = headLines + paneHeadLines
	leftPct       = 55
)

var (
	colorAccent  = lipgloss.Color("99")
	colorSelBg   = lipgloss.Color("63")
	colorFocusBg = lipgloss.Color("237")

	styleFrame   = lipgloss.NewStyle().Foreground(colorAccent)
	styleDim     = lipgloss.NewStyle().Faint(true)
	styleBlocked = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleWorking = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))
)

// rowBg is the background a row carries, applied per piece: wrapping already
// styled text in one outer style lets inner resets punch holes in the block.
// The cursor of the unfocused pane keeps the weaker block, so both panes show
// where they stand without competing (the frame_mock D24 treatment).
type rowBg int

const (
	bgNone rowBg = iota
	bgWeak
	bgStrong
)

func bgFor(cursor, focused bool) rowBg {
	switch {
	case cursor && focused:
		return bgStrong
	case cursor:
		return bgWeak
	}
	return bgNone
}

func (b rowBg) style(st lipgloss.Style) lipgloss.Style {
	switch b {
	case bgStrong:
		return st.Background(colorSelBg)
	case bgWeak:
		return st.Background(colorFocusBg)
	}
	return st
}

func (b rowBg) plain(s string) string { return b.style(lipgloss.NewStyle()).Render(s) }

// The marker glyphs, shared with frame_mock. They are named because their
// widths feed the layout, and those widths are measured — see glyphWidth.
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
// and padding the row with the other tears the name column apart. An explicit
// Condition also keeps this independent of the ambient locale and of
// package-global mutation.
var cells = &runewidth.Condition{EastAsianWidth: false, StrictEmojiNeutral: true}

// glyphWidth measures a raw glyph. lipgloss.Width stays for strings that have
// already been rendered: those carry ANSI escapes, which runewidth would count
// as printable.
func glyphWidth(s string) int { return cells.StringWidth(s) }

// dotGlyph and dotStyle split the status marker in two so its width can be
// taken before the escapes go on.
func dotGlyph(status string) string {
	if status == "unknown" || status == "" {
		return glyphHollow
	}
	return glyphFilled
}

func dotStyle(status string) lipgloss.Style {
	switch status {
	case "waiting":
		return styleBlocked
	case "working":
		return styleWorking
	default:
		return styleIdle
	}
}

// dot is frame_mock's status marker: red blocked, yellow working, green idle,
// hollow green when something is running but has reported nothing.
func dot(status string, bg rowBg) string {
	return bg.style(dotStyle(status)).Render(dotGlyph(status))
}

// spinnerFrames animates the in-flight marker (D29). This family is one cell
// wide under every runewidth condition; the ◐◓◑◒ half-circles were the other
// candidate but ◐ and ◑ are East-Asian Ambiguous, so two of their four frames
// would measure 2 without the cells pin — a spinner that changes width would
// jitter the whole name column as it turns.
var spinnerFrames = []string{"◜", "◝", "◞", "◟"}

// markerGlyph is the raw glyph the marker slot shows. Precedence: a bring-up
// in flight outranks everything, because it is the state the user just asked
// for and the one that is about to change; then an unread bell, then the
// status dot; nothing at all when the entry is neither running nor starting.
// marker renders exactly this glyph, so the width math and the drawing cannot
// pick different ones.
func markerGlyph(status string, bell, running, starting bool, frame int) string {
	switch {
	case starting:
		return spinnerFrames[frame%len(spinnerFrames)]
	case !running:
		return ""
	case bell:
		return glyphBell
	default:
		return dotGlyph(status)
	}
}

// marker is the one marker slot, rendered.
func marker(status string, bell, running, starting bool, frame int, bg rowBg) string {
	g := markerGlyph(status, bell, running, starting, frame)
	switch {
	case g == "":
		return ""
	case starting:
		// Yellow like `working`: something is happening and you did not ask
		// for it to need you.
		return bg.style(styleWorking).Render(g)
	case g == glyphBell:
		return bg.plain(g)
	default:
		return dot(status, bg)
	}
}

func (m model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}
	out, _, _ := m.screen()
	v := tea.NewView(out)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// paneMap is where a pane's rows landed, so a click reaches the row that was
// drawn rather than one computed independently.
type paneMap struct {
	x0, x1  int   // screen x range, x1 exclusive
	rowLine []int // screen y of each visible row
	cursor  []int // the row's index within its list
}

func (p paneMap) at(x, y int) (int, bool) {
	if x < p.x0 || x >= p.x1 {
		return 0, false
	}
	for k, ln := range p.rowLine {
		if y == ln {
			return p.cursor[k], true
		}
	}
	return 0, false
}

func (m model) budget() int {
	return max(m.height-paneTop-gapLines-len(m.footerLines()), 1)
}

func (m model) widths() (leftW, rightW int) {
	leftW = max(m.width*leftPct/100, 1)
	if m.width >= 24 {
		leftW = min(max(leftW, 12), m.width-12)
	}
	return leftW, max(m.width-leftW-1, 1)
}

// screen renders the whole window edge to edge (D27) and reports both panes'
// row maps.
func (m model) screen() (out string, left, right paneMap) {
	if m.mode == modeLanded {
		return m.renderLanded(), left, right
	}
	leftW, rightW := m.widths()
	f := m.matched()
	scope := "history"
	if m.filter != "" {
		scope = fmt.Sprintf("%d/%d", len(f), len(m.locs))
	}
	lines := []string{m.inputLine(scope), ""}

	leftRows, leftMap := m.leftPane(leftW, f)
	rightRows, rightMap := m.rightPane(rightW)
	head := m.paneHead("locations", leftW, m.focus == focusLeft) +
		styleDim.Render("│") + m.paneHead(m.rightTitle(), rightW, m.focus == focusRight)
	lines = append(lines, head)
	for i := range max(len(leftRows), len(rightRows)) {
		l, r := strings.Repeat(" ", leftW), ""
		if i < len(leftRows) {
			l = leftRows[i]
		}
		if i < len(rightRows) {
			r = rightRows[i]
		}
		lines = append(lines, l+styleDim.Render("│")+r)
	}
	foot := m.footerLines()
	for len(lines) < m.height-len(foot) {
		lines = append(lines, "")
	}
	lines = append(lines, foot...)
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	leftMap.x0, leftMap.x1 = 0, leftW
	rightMap.x0, rightMap.x1 = leftW+1, m.width
	// Nothing may exceed the window: a popup is narrow and a spilling line
	// would wrap, pushing every row below it off its own click.
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(lines, "\n")),
		leftMap, rightMap
}

// inputLine is the text area. Its focus has to be legible at a glance, since
// whether a letter types or acts depends on it: focused, the prompt is lit
// and carries a cursor block; unfocused, both go dim and the block is gone.
func (m model) inputLine(scope string) string {
	if m.focus != focusInput {
		text := m.filter
		if text == "" {
			text = "(no filter)"
		}
		return styleDim.Render("› "+text) + styleDim.Render("   "+scope)
	}
	return styleFrame.Render("› ") + m.filter + styleFrame.Render("▌") +
		styleDim.Render("   "+scope)
}

func (m model) paneHead(s string, w int, focused bool) string {
	st := styleDim
	if focused {
		st = styleFrame
	}
	return pad(st.Render(" "+s), w, bgNone)
}

func (m model) rightTitle() string {
	li, ok := m.currentLoc()
	if !ok {
		return "projects"
	}
	return "projects at " + m.locs[li].label()
}

// leftPane draws the locations: marker, git-aware label, and as much of the
// path as the column can spare.
func (m model) leftPane(w int, f []int) ([]string, paneMap) {
	var out []string
	var pm paneMap
	cost := make([]int, len(f))
	for i := range cost {
		cost[i] = 1
	}
	n := window(cost, m.leftOff, m.budget())
	for k := range n {
		idx := m.leftOff + k
		l := m.locs[f[idx]]
		bg := bgFor(idx == m.leftSel, m.focus == focusLeft)
		status, bell, running, starting := l.attention()
		mk := marker(status, bell, running, starting, m.spin, bg)
		gap := strings.Repeat(" ",
			markerSlot-glyphWidth(markerGlyph(status, bell, running, starting, m.spin)))
		row := bg.plain(" ") + mk +
			bg.style(lipgloss.NewStyle().Bold(idx == m.leftSel)).Render(gap+l.label())
		if tw := w - lipgloss.Width(row) - 2; tw >= 10 {
			tail := shortPath(l.dir, tw)
			row += bg.plain(strings.Repeat(" ", w-lipgloss.Width(row)-lipgloss.Width(tail)-1)) +
				bg.style(styleDim).Render(tail) + bg.plain(" ")
		}
		pm.rowLine = append(pm.rowLine, paneTop+len(out))
		pm.cursor = append(pm.cursor, idx)
		out = append(out, pad(row, w, bg))
	}
	if len(f) == 0 {
		out = append(out, pad(styleDim.Render("  no location matches"), w, bgNone))
	}
	if hidden := len(f) - m.leftOff - n; hidden > 0 {
		out = append(out, pad(styleDim.Render(fmt.Sprintf("  … %d more", hidden)), w, bgNone))
	}
	return out, pm
}

// rightPane draws the projects at the selected location: a toggle box, the
// running marker, the project name and the compose file it comes from.
func (m model) rightPane(w int) ([]string, paneMap) {
	var out []string
	var pm paneMap
	li, ok := m.currentLoc()
	if !ok {
		return out, pm
	}
	ps := m.locs[li].projects
	if len(ps) == 0 {
		return []string{pad(styleDim.Render("  no compose file here"), w, bgNone)}, pm
	}
	cost := m.rightCost(li)
	n := window(cost, m.rightOff, m.budget())
	for k := range n {
		idx := m.rightOff + k
		p := ps[idx]
		bg := bgFor(idx == m.rightSel, m.focus == focusRight)
		box := "[ ]"
		if p.enabled {
			box = "[x]"
		}
		mk := marker(p.status, p.bell, p.running, p.starting(), m.spin, bg)
		gap := strings.Repeat(" ", markerSlot-glyphWidth(
			markerGlyph(p.status, p.bell, p.running, p.starting(), m.spin)))
		row := bg.plain(" ") + bg.style(m.weakStyle()).Render(box) + bg.plain(" ") + mk +
			bg.style(lipgloss.NewStyle().Bold(idx == m.rightSel)).Render(gap+p.name)
		file := p.file
		if p.noMux {
			file += " · no mux"
		}
		if fw := w - lipgloss.Width(row) - 2; fw >= 6 {
			f := shortPath(file, fw)
			row += bg.plain(strings.Repeat(" ", w-lipgloss.Width(row)-lipgloss.Width(f)-1)) +
				bg.style(styleDim).Render(f) + bg.plain(" ")
		}
		pm.rowLine = append(pm.rowLine, paneTop+len(out))
		pm.cursor = append(pm.cursor, idx)
		out = append(out, pad(row, w, bg))
		if li == m.failedLoc && idx == m.failedPrj {
			// Keep-and-surface (D10): the row stays, the reason it cannot
			// land is spelled out under it, with the removal offer. pad
			// truncates from the right, which is what a message wants —
			// shortPath's keep-the-tail is for paths only.
			out = append(out, pad(styleBlocked.Render(
				"  ✖ missing: "+p.file+" · ctrl+d drops it"), w, bgNone))
		}
	}
	if hidden := len(ps) - m.rightOff - n; hidden > 0 {
		out = append(out, pad(styleDim.Render(fmt.Sprintf("  … %d more", hidden)), w, bgNone))
	}
	return out, pm
}

// pad fills a row to the column width so a highlighted row is a solid block,
// and truncates anything that would otherwise wrap and displace the rows
// under it.
func pad(s string, w int, bg rowBg) string {
	s = lipgloss.NewStyle().MaxWidth(w).Render(s)
	if fill := w - lipgloss.Width(s); fill > 0 {
		s += bg.plain(strings.Repeat(" ", fill))
	}
	return s
}

func (m model) footerLines() []string {
	var full string
	switch m.focus {
	case focusInput:
		full = "type to filter · tab complete · ↑↓ move · enter → locations · esc clear/quit"
	case focusLeft:
		full = "j/k move · enter → projects · s start · S jump · / input · esc back"
	case focusRight:
		full = "space toggle · s start enabled · S jump+attach · / input · esc ← locations"
	}
	if m.width < lipgloss.Width(full) {
		full = "enter → · j/k move · s start · S jump · / input · esc back"
	}
	hints := styleDim.Render(full)
	if m.note != "" {
		return []string{styleFrame.Render("· " + m.note), hints}
	}
	return []string{hints}
}

// renderLanded is the landing promise made visible (D10): the launcher is
// gone and the project is on screen. This screen is the focus switch into the
// mux window, not a progress view — so if the project was cold, its header
// carries the same spinner the launcher rows would (D29). D9's mux-less
// project lands too, on a synthesized shell window, and says why it looks bare.
func (m model) renderLanded() string {
	l := m.locs[m.landedLoc]
	p := l.projects[m.landedPrj]
	head := "▶ landed in " + l.label() + " · " + p.name
	if p.starting() {
		head += " " + markerGlyph(p.status, p.bell, p.running, true, m.spin) + " starting"
	}
	lines := []string{styleFrame.Render(head), ""}
	if p.noMux {
		lines = append(lines,
			styleWorking.Render("⚠ project is up, but its compose file has no mux"),
			styleWorking.Render("  section — opened a shell window at "+
				shortPath(l.dir, max(m.width-36, 8))),
			"",
			styleDim.Render("┌─ shell — "+shortPath(l.dir, max(m.width-14, 8))),
			styleDim.Render("│  $ "))
	} else {
		lines = append(lines,
			styleDim.Render("┌─ devserver — working · serving :8080"),
			styleDim.Render("│  … pane output …"),
			styleDim.Render("┌─ claude — waiting · review mon_run.go"),
			styleDim.Render("│  … pane output …"))
	}
	return m.fill(lines, "esc back to the launcher · q quit")
}

// fill pins a footer to the bottom of the window and pads the rest (D27).
func (m model) fill(lines []string, foot string) string {
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, styleDim.Render(foot))
	if len(lines) > m.height {
		lines = lines[:m.height]
	}
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(lines, "\n"))
}

// --- mouse ---

// clickAt moves the left cursor or toggles a project, whichever pane was hit,
// and focuses that pane so the keyboard carries on from where you clicked.
func (m model) clickAt(x, y int) model {
	if m.mode != modeList {
		return m
	}
	if y == 0 {
		// Clicking the text area puts the keyboard back in it.
		m.focus = focusInput
		return m
	}
	_, left, right := m.screen()
	if i, ok := left.at(x, y); ok {
		m.focus = focusLeft
		if i != m.leftSel {
			m.leftSel, m.rightSel, m.rightOff = i, 0, 0
		}
		return m.clampLeft()
	}
	if i, ok := right.at(x, y); ok {
		m.focus, m.rightSel = focusRight, i
		return m.clampRight().toggle()
	}
	return m
}

// wheel scrolls the pane the pointer is over, cursor unmoved — scrolling away
// from the cursor is what a wheel is for.
func (m model) wheel(msg tea.MouseWheelMsg) model {
	if m.mode != modeList {
		return m
	}
	d := wheelStep
	switch msg.Button {
	case tea.MouseWheelUp:
		d = -wheelStep
	case tea.MouseWheelDown:
	default:
		return m
	}
	leftW, _ := m.widths()
	if msg.X < leftW {
		n := len(m.matched())
		m.leftOff = min(max(m.leftOff+d, 0), max(n-1, 0))
		return m
	}
	li, ok := m.currentLoc()
	if !ok {
		return m
	}
	m.rightOff = min(max(m.rightOff+d, 0), max(len(m.locs[li].projects)-1, 0))
	return m
}

const wheelStep = 3

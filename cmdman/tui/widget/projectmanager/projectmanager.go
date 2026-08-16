// Package projectmanager implements the project-manager widget: the shortcut
// panel over one project's replica scale, shown-replica cycling and layout
// cycling. It is popup-summoned like the launcher and is not a frame component
// (D6).
//
// It is a standalone model rather than a panel facade: its keys are zoned
// (services list vs layouts list, with scale and cycle keys on top), so it
// shares no key handling with the docked widgets. Everything it shares with the
// other widgets lives in cmdman/tui/internal/core.
package projectmanager

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// managerZone is which of the two lists has the keyboard. Scale keys act on a
// service and layout keys on a layout, so every key that does something means
// exactly one thing once the zone is known.
type managerZone int

const (
	zoneServices managerZone = iota // the initial state
	zoneLayouts
)

// Model is the project-manager widget. It is the whole program when
// Options.Widget names it, not a view inside another.
type Model struct {
	backend core.Backend

	// ctx is the program-scoped context used for backend calls. A bubbletea
	// Update takes no context of its own, so the one New was given is held here;
	// a zero-value Model built by a test has none and bgCtx covers that.
	ctx context.Context

	width, height int
	altScreen     bool
	// noQuit unbinds the quit gestures, as it does for every other widget (V6).
	noQuit bool

	info   core.ProjectManagerInfo
	loaded bool
	// loadErr is the reason the project could not be loaded — the backend's
	// probe trail when nothing was detectable (D4). It is the body rather than a
	// footer line: with no project there is nothing else to render.
	loadErr string

	focus                managerZone
	svcSel, svcOff       int
	layoutSel, layoutOff int

	// pending names the action in flight and loading says a listing is, so a
	// slow backend call shows what it is doing instead of freezing. Both gate
	// the action keys: `+`/`-` compute from the loaded replica count (D11), and
	// a second action taken against a count that is already being replaced would
	// scale from a stale base.
	pending string
	loading bool

	// note is a transient one-liner, cleared on the next keystroke; errMsg is
	// the last failure, cleared the same way.
	note   string
	errMsg string

	quitting bool
}

// New constructs the project-manager model from the same options every other
// TUI mode takes. ctx is the program's, and is what the backend calls the model
// issues are made under.
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

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return loadCmd(m.bgCtx(), m.backend, "", "")
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.clamp(), nil
	case managerLoadedMsg:
		return m.onLoaded(msg), nil
	case scaleSetMsg:
		return m.onScaleSet(msg)
	case scaleCycledMsg:
		return m.onScaleCycled(msg)
	case layoutAppliedMsg:
		return m.onLayoutApplied(msg)
	case layoutCycledMsg:
		return m.onLayoutCycled(msg)
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

// --- backend replies ---------------------------------------------------------

// onLoaded takes the aggregate load. A load that never answered is the body
// (D4's probe trail); one that fails after the view was already drawn is a
// footer failure instead — the rows on screen are still the last thing the
// backend actually said.
func (m Model) onLoaded(msg managerLoadedMsg) Model {
	m.loading = false
	if msg.err != nil {
		if m.loaded {
			m.errMsg = "reload: " + msg.err.Error()
			return m
		}
		m.loadErr = msg.err.Error()
		return m
	}
	m.loaded, m.loadErr = true, ""
	m.info = msg.info
	return m.clamp()
}

// onScaleSet, onScaleCycled, onLayoutApplied and onLayoutCycled share one
// shape: the action is over, its outcome is said once, and a successful one is
// followed by a reload — the dashboard has changed, and the counts and markers
// the next key acts on are the ones it just changed.
func (m Model) onScaleSet(msg scaleSetMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.failed(fmt.Sprintf("scale %s: %v", msg.service, msg.err)), nil
	}
	return m.done(fmt.Sprintf("%s scaled to %d", msg.service, msg.replicas))
}

// onScaleCycled words a failure as a report rather than as a refusal: a stray
// identity-stamped window makes the cycle exit non-zero even when every real
// dashboard moved, so "did not happen" is a claim this call cannot make (D14).
func (m Model) onScaleCycled(msg scaleCycledMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.failed(fmt.Sprintf("cycle reported: %v", msg.err)), nil
	}
	return m.done("cycled shown replica of " + msg.service)
}

func (m Model) onLayoutApplied(msg layoutAppliedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.failed(fmt.Sprintf("layout %s: %v", msg.layout, msg.err)), nil
	}
	return m.done("applied layout " + msg.layout)
}

func (m Model) onLayoutCycled(msg layoutCycledMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.failed(fmt.Sprintf("cycle layout: %v", msg.err)), nil
	}
	return m.done("cycled layout")
}

func (m Model) failed(text string) Model {
	m.pending, m.errMsg = "", text
	return m
}

// done reports a finished action and reloads what it changed.
func (m Model) done(text string) (tea.Model, tea.Cmd) {
	m.pending, m.errMsg, m.note = "", "", text
	return m.reload()
}

func (m Model) reload() (tea.Model, tea.Cmd) {
	m.loading = true
	return m, loadCmd(m.bgCtx(), m.backend, m.info.Project, m.info.Path)
}

// --- keys --------------------------------------------------------------------

// onKey handles the widget's key table. Movement, the zone switch and the quit
// gesture are always live; the action keys are the focused list's alone and
// wait for whatever is in flight.
func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.note, m.errMsg = "", ""
	switch msg.String() {
	case "q", "ctrl+c":
		if m.noQuit {
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "j", "down":
		return m.move(1), nil
	case "k", "up":
		return m.move(-1), nil
	case "tab":
		if m.focus == zoneServices {
			m.focus = zoneLayouts
		} else {
			m.focus = zoneServices
		}
		return m.clamp(), nil
	}
	if m.busy() {
		return m, nil
	}
	switch msg.String() {
	case "+", "=":
		return m.setScale(1)
	case "-":
		return m.setScale(-1)
	case "l", "right":
		return m.cycleScale(1)
	case "h", "left":
		return m.cycleScale(-1)
	case "enter":
		return m.applyLayout()
	case "c":
		return m.cycleLayout()
	case "r":
		return m.reload()
	}
	return m, nil
}

// busy reports that a backend call is in flight. A failed load is not busy: `r`
// is the only way back from it.
func (m Model) busy() bool { return m.pending != "" || m.loading }

func (m Model) move(d int) Model {
	if m.focus == zoneLayouts {
		if n := len(m.info.Layouts.Names); n > 0 {
			m.layoutSel = (m.layoutSel + d + n) % n
		}
		return m.clamp()
	}
	if n := len(m.info.Services); n > 0 {
		m.svcSel = (m.svcSel + d + n) % n
	}
	return m.clamp()
}

func (m Model) service() (core.ServiceScaleInfo, bool) {
	if m.focus != zoneServices || m.svcSel < 0 || m.svcSel >= len(m.info.Services) {
		return core.ServiceScaleInfo{}, false
	}
	return m.info.Services[m.svcSel], true
}

func (m Model) layout() (string, bool) {
	if m.focus != zoneLayouts || m.layoutSel < 0 || m.layoutSel >= len(m.info.Layouts.Names) {
		return "", false
	}
	return m.info.Layouts.Names[m.layoutSel], true
}

// setScale is `+`/`-`: the replica count the row shows, one step either way.
// The base is the row's live instance count (D11), which is why the reload
// after every action matters — a `+` computed from a superseded count would
// undo the scale before it.
func (m Model) setScale(d int) (tea.Model, tea.Cmd) {
	svc, ok := m.service()
	if !ok {
		return m, nil
	}
	want := svc.Replicas + d
	if want < 0 {
		m.note = svc.Name + " is already at 0 replicas"
		return m, nil
	}
	m.pending = fmt.Sprintf("scaling %s to %d…", svc.Name, want)
	return m, setScaleCmd(
		m.bgCtx(), m.backend, m.info.Project, m.info.Path, m.info.WorkDir, svc.Name, want,
	)
}

// cycleScale is `l`/`h`: which replica the service's dashboard pane shows.
// Forward is CycleScale's own "advance by one" (set 0). Backward has no
// backend gesture, so it is spelled as the 1-based replica before the shown one
// — which needs a shown one: an unknown position (D14's disagreement, or no
// dashboard at all) has no predecessor to name, and guessing one would move the
// pane somewhere the user did not ask for.
func (m Model) cycleScale(d int) (tea.Model, tea.Cmd) {
	svc, ok := m.service()
	if !ok {
		return m, nil
	}
	if !svc.Cyclable {
		m.note = svc.Name + " is not a cycle target in this project's mux layouts"
		return m, nil
	}
	set := 0
	if d < 0 {
		if svc.Shown <= 0 || svc.Replicas <= 0 {
			m.note = "shown replica of " + svc.Name + " is unknown — l advances it"
			return m, nil
		}
		set = svc.Shown - 1
		if set == 0 {
			set = svc.Replicas
		}
	}
	m.pending = "cycling shown replica of " + svc.Name + "…"
	return m, cycleScaleCmd(
		m.bgCtx(), m.backend, m.info.Project, m.info.Path, m.info.WorkDir, svc.Name, set,
	)
}

func (m Model) applyLayout() (tea.Model, tea.Cmd) {
	name, ok := m.layout()
	if !ok {
		return m, nil
	}
	m.pending = "applying layout " + name + "…"
	return m, applyLayoutCmd(
		m.bgCtx(), m.backend, m.info.Project, m.info.Path, m.info.WorkDir, name,
	)
}

func (m Model) cycleLayout() (tea.Model, tea.Cmd) {
	if m.focus != zoneLayouts {
		return m, nil
	}
	if len(m.info.Layouts.Names) == 0 {
		m.note = "no mux layouts for " + m.projectLabel()
		return m, nil
	}
	m.pending = "cycling layout…"
	return m, cycleLayoutCmd(m.bgCtx(), m.backend, m.info.Project, m.info.Path, m.info.WorkDir)
}

func (m Model) projectLabel() string {
	if m.info.Project == "" {
		return "this project"
	}
	return m.info.Project
}

package projectmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"
)

// managerFixture is the shape the widget was designed against: a cycle target
// whose shown replica is known, one that is not a cycle target at all, a
// service that has never been created (D11's zero base), and a cycle target
// whose windows do not agree on a position (D14's unknown).
func managerFixture() core.ProjectManagerInfo {
	return core.ProjectManagerInfo{
		Project: "api",
		Path:    "/home/u/src/api/compose.yaml",
		Services: []core.ServiceScaleInfo{
			{Name: "web", Replicas: 3, Shown: 2, Cyclable: true},
			{Name: "db", Replicas: 1, Cyclable: false},
			{Name: "worker", Replicas: 0, Cyclable: true},
		},
		Layouts: core.LayoutsInfo{
			Project: "api",
			Path:    "/home/u/src/api/compose.yaml",
			Names:   []string{"default", "wide", "debug"},
			Current: 1,
		},
	}
}

func seedManager(t *testing.T, width, height int) (Model, *coretest.FakeBackend) {
	t.Helper()
	fb := &coretest.FakeBackend{ManagerInfo: managerFixture()}
	m := New(
		context.Background(),
		core.Options{Backend: fb, Widget: core.WidgetProjectManager, Version: "v0.0.0-test"},
	)
	m = updManager(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	drainManagerCmd(t, m.Init())
	return updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo}), fb
}

func updManager(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return got
}

// pressManager drives one key and runs whatever it asked the backend for, the
// way the program would: the reply is handed back so the caller can feed it in.
func pressManager(t *testing.T, m Model, msg tea.Msg) (Model, tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	if cmd == nil {
		return got, nil
	}
	return got, cmd()
}

func drainManagerCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return cmd()
}

func plainView(m Model) string { return core.StripANSI(m.viewContent()) }

// TestManagerLoadsOnOpen pins the opening state: one aggregate load with no
// project of its own — the backend resolves which project this is (D3) — and
// the view names it once the answer lands.
func TestManagerLoadsOnOpen(t *testing.T) {
	fb := &coretest.FakeBackend{ManagerInfo: managerFixture()}
	m := New(context.Background(), core.Options{Backend: fb})
	m = updManager(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})

	if out := plainView(m); !strings.Contains(out, "loading…") {
		t.Errorf("an unanswered load should say so:\n%s", out)
	}
	msg := drainManagerCmd(t, m.Init())
	if len(fb.ManagerReq) != 1 || fb.ManagerReq[0] != "" {
		t.Fatalf("the opening load asked for %v, want one call with no project", fb.ManagerReq)
	}
	m = updManager(t, m, msg)

	out := plainView(m)
	for _, want := range []string{"project api", "web", "db", "worker", "default", "wide"} {
		if !strings.Contains(out, want) {
			t.Errorf("the view should carry %q:\n%s", want, out)
		}
	}
}

// TestManagerBadgesAndMarkers covers what a row says at a glance: the replica
// count the +/- keys work from, the shown replica, a cycle target whose
// position nothing agrees on (D14's unknown), and the dashboard's own layout.
func TestManagerBadgesAndMarkers(t *testing.T) {
	m, _ := seedManager(t, 80, 24)
	out := plainView(m)

	for _, want := range []string{"↻ web", "×3", "[2]", "↻ worker", "[?]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the services list should carry %q:\n%s", want, out)
		}
	}
	// db is not a cycle target: it has no shown replica to be unknown about.
	if strings.Contains(out, "↻ db") {
		t.Errorf("a service that is no cycle target should carry no cycle marker:\n%s", out)
	}
	if got := shownBadge(core.ServiceScaleInfo{Name: "db", Replicas: 1}); got != "" {
		t.Errorf("shownBadge of a non-target = %q, want empty", got)
	}
	if !strings.Contains(out, core.GlyphFilled+" wide") {
		t.Errorf("the dashboard's own layout should be marked:\n%s", out)
	}
}

// TestManagerMovementAndZone pins the key table's navigation half: j/k and the
// arrows move within the focused list only, and tab is what changes which list
// that is.
func TestManagerMovementAndZone(t *testing.T) {
	m, _ := seedManager(t, 80, 24)

	m = updManager(t, m, coretest.Kr("j"))
	if m.svcSel != 1 || m.layoutSel != 0 {
		t.Fatalf("j in the services zone = svc %d layout %d, want svc 1 layout 0",
			m.svcSel, m.layoutSel)
	}
	m = updManager(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updManager(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.svcSel != 0 {
		t.Errorf("moving past the last service should wrap, got %d", m.svcSel)
	}
	m = updManager(t, m, coretest.Kr("k"))
	if m.svcSel != len(m.info.Services)-1 {
		t.Errorf("k off the top should wrap to the last service, got %d", m.svcSel)
	}

	m = updManager(t, m, coretest.KTab)
	if m.focus != zoneLayouts {
		t.Fatalf("tab should move the keyboard to the layouts list")
	}
	svcBefore := m.svcSel
	m = updManager(t, m, coretest.Kr("j"))
	if m.layoutSel != 1 || m.svcSel != svcBefore {
		t.Errorf("j in the layouts zone = svc %d layout %d, want svc %d layout 1",
			m.svcSel, m.layoutSel, svcBefore)
	}
	if m = updManager(t, m, coretest.KTab); m.focus != zoneServices {
		t.Errorf("tab should come back to the services list")
	}
}

// TestManagerSetScale pins +/- : the count asked for is the row's live replica
// count one step either way (D11's base), and 0 has no step down.
func TestManagerSetScale(t *testing.T) {
	m, fb := seedManager(t, 80, 24)

	m, msg := pressManager(t, m, coretest.Kr("+"))
	if m.pending == "" {
		t.Errorf("a scale in flight should show a pending line, got none")
	}
	m = updManager(t, m, msg)
	want := coretest.ScaleCall{Project: "api", Service: "web", Replicas: 4}
	if len(fb.ScalesSet) != 1 || fb.ScalesSet[0] != want {
		t.Fatalf("+ recorded %v, want [%v]", fb.ScalesSet, want)
	}

	m = updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo})
	m, msg = pressManager(t, m, coretest.Kr("-"))
	m = updManager(t, m, msg)
	want = coretest.ScaleCall{Project: "api", Service: "web", Replicas: 2}
	if len(fb.ScalesSet) != 2 || fb.ScalesSet[1] != want {
		t.Fatalf("- recorded %v, want [.., %v]", fb.ScalesSet, want)
	}

	// `=` is `+` without the shift, the key table's second spelling.
	m = updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo})
	m, msg = pressManager(t, m, coretest.Kr("="))
	m = updManager(t, m, msg)
	if len(fb.ScalesSet) != 3 || fb.ScalesSet[2].Replicas != 4 {
		t.Fatalf("= recorded %v, want a third call to 4 replicas", fb.ScalesSet)
	}

	// worker has never been created: there is no replica to take away.
	m = updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo})
	m = updManager(t, m, coretest.Kr("j"))
	m = updManager(t, m, coretest.Kr("j"))
	m, _ = pressManager(t, m, coretest.Kr("-"))
	if len(fb.ScalesSet) != 3 {
		t.Fatalf("- at 0 replicas should call nothing, got %v", fb.ScalesSet)
	}
	if !strings.Contains(m.note, "already at 0") {
		t.Errorf("- at 0 replicas should say so, note = %q", m.note)
	}
}

// TestManagerCycleScale pins l/h: forward is CycleScale's own advance (set 0),
// backward names the replica before the shown one because CycleScale has no
// previous — and an unknown position has no predecessor to name (D14).
func TestManagerCycleScale(t *testing.T) {
	m, fb := seedManager(t, 80, 24)

	m, msg := pressManager(t, m, coretest.Kr("l"))
	m = updManager(t, m, msg)
	want := coretest.CycleScaleCall{Project: "api", Command: "web", Set: 0}
	if len(fb.ScalesCycled) != 1 || fb.ScalesCycled[0] != want {
		t.Fatalf("l recorded %v, want [%v]", fb.ScalesCycled, want)
	}

	m = updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo})
	m, msg = pressManager(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updManager(t, m, msg)
	want = coretest.CycleScaleCall{Project: "api", Command: "web", Set: 1}
	if len(fb.ScalesCycled) != 2 || fb.ScalesCycled[1] != want {
		t.Fatalf("left recorded %v, want [.., %v]", fb.ScalesCycled, want)
	}

	// Backward off the first replica wraps to the last one.
	wrapped := managerFixture()
	wrapped.Services[0].Shown = 1
	m = updManager(t, m, managerLoadedMsg{info: wrapped})
	m, msg = pressManager(t, m, coretest.Kr("h"))
	m = updManager(t, m, msg)
	want = coretest.CycleScaleCall{Project: "api", Command: "web", Set: 3}
	if len(fb.ScalesCycled) != 3 || fb.ScalesCycled[2] != want {
		t.Fatalf("h at replica 1 recorded %v, want [.., %v]", fb.ScalesCycled, want)
	}

	// An unknown shown replica has no previous, and a service that is no cycle
	// target has neither: both stay local rather than asking the backend.
	m = updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo})
	m = updManager(t, m, coretest.Kr("j"))
	m = updManager(t, m, coretest.Kr("j")) // worker: a cycle target, position unknown
	m, _ = pressManager(t, m, coretest.Kr("h"))
	if len(fb.ScalesCycled) != 3 || !strings.Contains(m.note, "unknown") {
		t.Fatalf("h with an unknown position recorded %v, note %q", fb.ScalesCycled, m.note)
	}
	m = updManager(t, m, coretest.Kr("k")) // db: no cycle target at all
	m, _ = pressManager(t, m, coretest.Kr("l"))
	if len(fb.ScalesCycled) != 3 || !strings.Contains(m.note, "not a cycle target") {
		t.Fatalf("l on a non-target recorded %v, note %q", fb.ScalesCycled, m.note)
	}
}

// TestManagerLayoutKeys pins enter and c as the layouts zone's alone: in the
// services zone neither reaches the backend.
func TestManagerLayoutKeys(t *testing.T) {
	m, fb := seedManager(t, 80, 24)

	m, _ = pressManager(t, m, coretest.KEnter)
	if len(fb.AppliedLayouts) != 0 {
		t.Fatalf("enter in the services zone applied %v, want nothing", fb.AppliedLayouts)
	}
	m, _ = pressManager(t, m, coretest.Kr("c"))
	if len(fb.MuxCycled) != 0 {
		t.Fatalf("c in the services zone cycled %v, want nothing", fb.MuxCycled)
	}

	m = updManager(t, m, coretest.KTab)
	m = updManager(t, m, coretest.Kr("j")) // the "wide" row
	m, msg := pressManager(t, m, coretest.KEnter)
	m = updManager(t, m, msg)
	if len(fb.AppliedLayouts) != 1 || fb.AppliedLayouts[0] != "wide" {
		t.Fatalf("enter applied %v, want [wide]", fb.AppliedLayouts)
	}

	m = updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo})
	m, msg = pressManager(t, m, coretest.Kr("c"))
	updManager(t, m, msg)
	if len(fb.MuxCycled) != 1 || fb.MuxCycled[0] != "api" {
		t.Fatalf("c cycled %v, want [api]", fb.MuxCycled)
	}
}

// TestManagerReloadsAfterEveryAction pins the immediate-feedback rule: an
// action that landed is followed by a fresh load of the project it changed, so
// the next +/- works from the count it produced rather than the one before it.
func TestManagerReloadsAfterEveryAction(t *testing.T) {
	for _, tc := range []struct {
		name string
		done tea.Msg
	}{
		{"scale", scaleSetMsg{service: "web", replicas: 4}},
		{"cycle scale", scaleCycledMsg{service: "web"}},
		{"apply layout", layoutAppliedMsg{layout: "wide"}},
		{"cycle layout", layoutCycledMsg{}},
	} {
		m, fb := seedManager(t, 80, 24)
		before := len(fb.ManagerReq)

		next, cmd := m.Update(tc.done)
		m = next.(Model)
		if !m.loading {
			t.Errorf("%s: a finished action should show its reload in flight", tc.name)
		}
		drainManagerCmd(t, cmd)
		if len(fb.ManagerReq) != before+1 {
			t.Fatalf("%s: reload calls = %d, want %d", tc.name, len(fb.ManagerReq), before+1)
		}
		// The reload names the project the widget landed on, so it stays on it.
		if got := fb.ManagerReq[before]; got != "api" {
			t.Errorf("%s: reload asked for %q, want api", tc.name, got)
		}
	}
}

// TestManagerBusyGatesActions pins the pending state: while a call is in
// flight the widget stays usable — movement and the zone switch keep working —
// but a second action does not stack onto a replica count that is already
// being replaced.
func TestManagerBusyGatesActions(t *testing.T) {
	m, fb := seedManager(t, 80, 24)

	next, _ := m.Update(coretest.Kr("+"))
	m = next.(Model)
	if out := plainView(m); !strings.Contains(out, "scaling web to 4…") {
		t.Errorf("an action in flight should show what it is doing:\n%s", out)
	}
	m, _ = pressManager(t, m, coretest.Kr("+"))
	if len(fb.ScalesSet) != 0 {
		t.Fatalf("a second + while one is in flight recorded %v, want nothing", fb.ScalesSet)
	}
	m = updManager(t, m, coretest.Kr("j"))
	if m.svcSel != 1 {
		t.Errorf("movement should stay live while an action is in flight")
	}

	// The reload the action leaves behind gates the action keys too, for the
	// same reason: its answer is the base the next + works from.
	m = updManager(t, m, scaleSetMsg{service: "web", replicas: 4})
	m, _ = pressManager(t, m, coretest.Kr("+"))
	if len(fb.ScalesSet) != 0 {
		t.Fatalf("+ during the reload recorded %v, want nothing", fb.ScalesSet)
	}
	m = updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo})
	m, _ = pressManager(t, m, coretest.Kr("+"))
	if len(fb.ScalesSet) != 1 {
		t.Fatalf("+ after the reload recorded %v, want one call", fb.ScalesSet)
	}
}

// TestManagerCycleFailureWording pins D14: a stray identity-stamped window
// makes cycle-scale exit non-zero even when every real dashboard moved, so the
// error line reports what came back instead of claiming nothing happened.
func TestManagerCycleFailureWording(t *testing.T) {
	m, _ := seedManager(t, 80, 24)
	m = updManager(t, m, scaleCycledMsg{
		service: "web",
		err:     errors.New("window @9: no pane for web"),
	})

	out := plainView(m)
	if !strings.Contains(out, "cycle reported: window @9: no pane for web") {
		t.Fatalf("the cycle error should be reported verbatim:\n%s", out)
	}
	if strings.Contains(out, "fail") {
		t.Errorf("the cycle error must not claim the cycle did not happen:\n%s", out)
	}
	if m.loading {
		t.Errorf("a failed action should not reload: nothing said the view is stale")
	}
}

// TestManagerActionFailuresSurface covers the other three actions' error line —
// they can honestly say the action failed, which the cycle's cannot.
func TestManagerActionFailuresSurface(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		msg  tea.Msg
		want string
	}{
		{scaleSetMsg{service: "web", replicas: 4, err: boom}, "scale web: boom"},
		{layoutAppliedMsg{layout: "wide", err: boom}, "layout wide: boom"},
		{layoutCycledMsg{err: boom}, "cycle layout: boom"},
	} {
		m, _ := seedManager(t, 80, 24)
		m = updManager(t, m, tc.msg)
		if out := plainView(m); !strings.Contains(out, tc.want) {
			t.Errorf("the failure line should carry %q:\n%s", tc.want, out)
		}
		if m.pending != "" {
			t.Errorf("a failed action should not stay pending")
		}
	}
}

// TestManagerNoProjectRendersTheProbeTrail pins D4: a load that found no
// project renders the backend's message — which names each probe that failed —
// as the body, and r is the way back from it.
func TestManagerNoProjectRendersTheProbeTrail(t *testing.T) {
	fb := &coretest.FakeBackend{
		ManagerErr: errors.New(
			"no project: mux token @9 names no cmdman window; window: none; cwd /tmp: no match"),
	}
	m := New(context.Background(), core.Options{Backend: fb})
	m = updManager(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updManager(t, m, drainManagerCmd(t, m.Init()))

	// The trail is wrapped rather than cut, so it is read back with the line
	// breaks the popup width put in it collapsed away.
	flat := strings.Join(strings.Fields(plainView(m)), " ")
	want := "no project: mux token @9 names no cmdman window; window: none; cwd /tmp: no match"
	if !strings.Contains(flat, want) {
		t.Fatalf("the body should carry the whole probe trail:\n%s", plainView(m))
	}

	pressManager(t, m, coretest.Kr("r"))
	if len(fb.ManagerReq) != 2 {
		t.Errorf("r after a failed load should try again, calls = %d", len(fb.ManagerReq))
	}
}

// TestManagerReloadFailureKeepsTheView pins the other half of that rule: once
// the view has been drawn, a failed reload is a footer failure — the rows on
// screen are still the last thing the backend actually said.
func TestManagerReloadFailureKeepsTheView(t *testing.T) {
	m, _ := seedManager(t, 80, 24)
	m = updManager(t, m, managerLoadedMsg{err: errors.New("store is gone")})

	out := plainView(m)
	if !strings.Contains(out, "reload: store is gone") {
		t.Fatalf("a failed reload should surface as a failure line:\n%s", out)
	}
	if !strings.Contains(out, "web") {
		t.Errorf("a failed reload should keep the rows it could not replace:\n%s", out)
	}
}

// TestManagerQuit pins V6: q and ctrl+c end a standalone run, and --no-quit
// takes both gestures away.
func TestManagerQuit(t *testing.T) {
	for _, key := range []tea.Msg{
		coretest.Kr("q"),
		tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
	} {
		m, _ := seedManager(t, 80, 24)
		next, cmd := m.Update(key)
		if cmd == nil || !coretest.MsgIsQuit(cmd()) {
			t.Errorf("%v should quit a standalone run", key)
		}
		if !next.(Model).quitting {
			t.Errorf("%v should mark the model quitting", key)
		}

		nq := New(context.Background(), core.Options{
			Backend: &coretest.FakeBackend{}, NoQuit: true,
		})
		if _, cmd := nq.Update(key); cmd != nil {
			t.Errorf("%v under --no-quit should not quit", key)
		}
	}
}

// TestManagerHintsFollowTheZone: the keys on offer are the focused list's, so
// the footer says which they are.
func TestManagerHints(t *testing.T) {
	m, _ := seedManager(t, 100, 24)
	if got := m.hintText(); !strings.Contains(got, "+/- replicas") ||
		!strings.Contains(got, "q quit") {
		t.Errorf("the services hints = %q", got)
	}
	m = updManager(t, m, coretest.KTab)
	if got := m.hintText(); !strings.Contains(got, "enter apply") {
		t.Errorf("the layouts hints = %q", got)
	}
	m.noQuit = true
	if got := m.hintText(); strings.Contains(got, "quit") {
		t.Errorf("a widget with no quit gesture should not offer one: %q", got)
	}
}

// TestManagerScrollsBothLists pins the windowing: a list longer than its share
// of the popup scrolls under its cursor and says how much it left out — out of
// its own share, so what it hides is rows and never the footer.
func TestManagerScrollsBothLists(t *testing.T) {
	fb := &coretest.FakeBackend{ManagerInfo: managerFixture()}
	for i := range 20 {
		fb.ManagerInfo.Services = append(fb.ManagerInfo.Services,
			core.ServiceScaleInfo{Name: "svc" + string(rune('a'+i)), Replicas: 1})
		fb.ManagerInfo.Layouts.Names = append(fb.ManagerInfo.Layouts.Names,
			"layout"+string(rune('a'+i)))
	}
	m := New(context.Background(), core.Options{Backend: fb})
	m = updManager(t, m, tea.WindowSizeMsg{Width: 60, Height: 14})
	m = updManager(t, m, managerLoadedMsg{info: fb.ManagerInfo})

	if out := plainView(m); !strings.Contains(out, "more") {
		t.Fatalf("a list that does not fit should say what it left out:\n%s", out)
	}
	for range len(fb.ManagerInfo.Services) - 1 {
		m = updManager(t, m, coretest.Kr("j"))
	}
	out := plainView(m)
	last := fb.ManagerInfo.Services[len(fb.ManagerInfo.Services)-1].Name
	if !strings.Contains(out, last) {
		t.Errorf("the cursor at the end of the list should be on screen:\n%s", out)
	}
	if !strings.Contains(out, "j/k move") {
		t.Errorf("two overflowing lists must not push the hints off screen:\n%s", out)
	}
	// The view never overflows the window it was given.
	if lines := strings.Split(m.viewContent(), "\n"); len(lines) > 14 {
		t.Errorf("the view drew %d lines into a 14-line window", len(lines))
	}

	// The footer's other line is the one that must survive most: a failure the
	// window pushed off screen is a failure the user cannot act on.
	m = updManager(t, m, scaleCycledMsg{service: "web", err: errors.New("no pane")})
	if out := plainView(m); !strings.Contains(out, "cycle reported: no pane") {
		t.Errorf("a failure must survive two overflowing lists:\n%s", out)
	}
}

func TestNewCarriesContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "threaded")
	m := New(ctx, core.Options{Backend: &coretest.FakeBackend{}})
	if m.ctx == nil || m.ctx.Value(ctxKey{}) == nil {
		t.Errorf("New should carry the caller's context into the model")
	}
}

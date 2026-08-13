package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// launcherFixture is the shape the launcher was designed against: history
// locations in recency order, one of them stale, plus a location that was never
// launched from and therefore shows up only once the filter reaches it.
func launcherFixture() []LaunchLocation {
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	return []LaunchLocation{
		{
			Dir: "/home/u/gitrepo/cmdman", RepoName: "cmdman",
			RepoURI: "github.com/ngicks/cmdman", Branch: "main",
			LastUsed: base, FromHistory: true,
			Projects: []LaunchProject{
				{
					Name:        "devenv",
					File:        "devenv.yaml",
					FromHistory: true,
					Running:     true,
					HasMux:      true,
				},
				{Name: "test", File: "test.yaml", HasMux: true},
			},
		},
		{
			Dir: "/home/u/src/webapp", RepoName: "webapp",
			RepoURI: "github.com/acme/webapp", Branch: "feat/auth",
			LastUsed: base.Add(-time.Hour), FromHistory: true,
			Projects: []LaunchProject{
				{Name: "staging", File: "compose.yaml", FromHistory: true, HasMux: true},
				{Name: "prod", File: "prod.yaml", HasMux: true},
			},
		},
		{
			Dir: "/home/u/old/demo-stack", LastUsed: base.Add(-2 * time.Hour), FromHistory: true,
			Projects: []LaunchProject{
				{
					Name: "demo", File: "gone.yaml", FromHistory: true,
					Problem: "missing: gone.yaml", Missing: true,
				},
			},
		},
		{
			// Never launched from: discovered on disk, so it is not in history.
			Dir: "/home/u/src/blog", RepoName: "blog", Branch: "main",
			Projects: []LaunchProject{{Name: "blog", File: "compose.yaml", HasMux: true}},
		},
	}
}

func seedLauncher(t *testing.T, width, height int) (launcherModel, *coretest.FakeBackend) {
	t.Helper()
	fb := &coretest.FakeBackend{LaunchLocs: launcherFixture()}
	m := newLauncher(core.Options{Backend: fb, Widget: core.WidgetLauncher, Version: "v0.0.0-test"})
	m = updLauncher(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	return updLauncher(t, m, launchTargetsLoadedMsg{locs: fb.LaunchLocs}), fb
}

func updLauncher(t *testing.T, m launcherModel, msg tea.Msg) launcherModel {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(launcherModel)
	if !ok {
		t.Fatalf("Update returned %T, want launcherModel", next)
	}
	return got
}

// typeInto drives literal keystrokes, the way a user produces them.
func typeInto(t *testing.T, m launcherModel, text string) launcherModel {
	t.Helper()
	for _, r := range text {
		m = updLauncher(t, m, coretest.Kr(string(r)))
	}
	return m
}

// leftLabels is what the left pane currently lists, by location label.
func leftLabels(m launcherModel) []string {
	var out []string
	for _, i := range m.matched() {
		out = append(out, m.locs[i].label())
	}
	return out
}

// TestLauncherHistoryIsTheOpeningList pins D7/D28: the empty input shows the
// history locations in the recency order the backend handed over, and a location
// that was never launched from appears only once the filter reaches it.
func TestLauncherHistoryIsTheOpeningList(t *testing.T) {
	m, _ := seedLauncher(t, 100, 20)

	want := []string{"cmdman(main)", "webapp(feat/auth)", "demo-stack"}
	if got := leftLabels(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("history list = %v, want %v", got, want)
	}

	m = typeInto(t, m, "blog")
	if got := leftLabels(m); len(got) != 1 || got[0] != "blog(main)" {
		t.Fatalf("filtering to a never-launched location = %v, want [blog(main)]", got)
	}
}

// TestLauncherMatchFields covers D18's match surface: a row survives on its
// branch, its repo uri or name, its full path, or one of its project names — and
// the field that matched is reported, so each is exercised independently.
func TestLauncherMatchFields(t *testing.T) {
	m, _ := seedLauncher(t, 100, 20)
	cmdmanLoc, webapp := m.locs[0], m.locs[1]

	for _, tc := range []struct {
		filter string
		loc    launcherLocation
		field  string
	}{
		{"feat/auth", webapp, "branch"},
		{"github.com/ngicks", cmdmanLoc, "repo"},
		{"webapp", webapp, "repo"},
		{"gitrepo/cmdman", cmdmanLoc, "path"},
		{"prod", webapp, "project"},
	} {
		field, ok := matchLaunchLocation(tc.filter, tc.loc)
		if !ok {
			t.Errorf("filter %q should match %s", tc.filter, tc.loc.Dir)
			continue
		}
		if field != tc.field {
			t.Errorf("filter %q matched on %q, want %q", tc.filter, field, tc.field)
		}
	}
	if _, ok := matchLaunchLocation("zzz", cmdmanLoc); ok {
		t.Errorf("filter %q should match nothing", "zzz")
	}
}

// TestLauncherInputZoneNeverActs is D28's amendment as a regression: history mode
// is the opening state, so the first keystrokes of natural queries — `src`,
// `staging` — must edit the filter and never start or launch anything. The
// original "s acts while the input is empty" rule failed exactly here.
func TestLauncherInputZoneNeverActs(t *testing.T) {
	for _, query := range []string{"src", "staging", "sS"} {
		m, fb := seedLauncher(t, 100, 20)
		m = typeInto(t, m, query)

		if m.filter != query {
			t.Errorf("typing %q left filter %q", query, m.filter)
		}
		if m.focus != zoneInput {
			t.Errorf("typing %q left focus %d, want the input zone", query, m.focus)
		}
		if len(fb.StartedProjects) != 0 || len(fb.LaunchedProjects) != 0 {
			t.Errorf("typing %q started %v and launched %v",
				query, fb.StartedProjects, fb.LaunchedProjects)
		}
	}
}

// TestLauncherZoneWalking walks the three zones the way the keys promise: enter
// steps in, esc steps back and then clears the query before quitting (D31), `/`
// jumps to the input and ctrl+u erases it from anywhere.
func TestLauncherZoneWalking(t *testing.T) {
	m, _ := seedLauncher(t, 100, 20)

	m = updLauncher(t, m, coretest.KEnter)
	if m.focus != zoneLeft {
		t.Fatalf("enter from the input = zone %d, want left", m.focus)
	}
	m = updLauncher(t, m, coretest.KEnter)
	if m.focus != zoneRight {
		t.Fatalf("enter from the left list = zone %d, want right", m.focus)
	}
	m = updLauncher(t, m, kEsc)
	if m.focus != zoneLeft {
		t.Fatalf("esc from the right list = zone %d, want left", m.focus)
	}
	m = updLauncher(t, m, kEsc)
	if m.focus != zoneInput {
		t.Fatalf("esc from the left list = zone %d, want input", m.focus)
	}

	// `/` reaches the input from a list, and only from a list: in the input it is
	// a character like any other.
	m = updLauncher(t, m, coretest.KEnter)
	m = updLauncher(t, m, coretest.Kr("/"))
	if m.focus != zoneInput {
		t.Fatalf("/ from a list = zone %d, want input", m.focus)
	}

	m = typeInto(t, m, "web")
	m = updLauncher(t, m, kEsc)
	if m.filter != "" || m.focus != zoneInput {
		t.Fatalf("esc in the input should clear the query first, got %q / zone %d",
			m.filter, m.focus)
	}
	next, cmd := m.Update(kEsc)
	if !next.(launcherModel).quitting || cmd == nil {
		t.Fatalf("esc on an empty query should dismiss the launcher")
	}

	m = typeInto(t, m, "web")
	m = updLauncher(t, m, coretest.KEnter)
	m = updLauncher(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.filter != "" || m.focus != zoneInput {
		t.Fatalf("ctrl+u should erase the query and return to it, got %q / zone %d",
			m.filter, m.focus)
	}
}

// TestLauncherStartEnabledSkipsRunning pins `s` (D4/D28/D31): it starts every
// enabled project that is not already up, skips the ones that are, and marks the
// rows it started as starting so the spinner has something to show (D29).
func TestLauncherStartEnabledSkipsRunning(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	// Both projects at the first location enabled; devenv is already running.
	m = updLauncher(t, m, coretest.KEnter) // into the left list
	m.locs[0].projects[1].enabled = true

	next, cmd := m.Update(coretest.Kr("s"))
	m = next.(launcherModel)
	if cmd == nil {
		t.Fatalf("s should issue work")
	}
	drainLauncherCmd(t, cmd)

	if len(fb.StartedProjects) != 1 || fb.StartedProjects[0].Project != "test" {
		t.Fatalf("s started %v, want only the project that was not running", fb.StartedProjects)
	}
	if m.locs[0].projects[0].starting {
		t.Errorf("a running project should not be marked starting")
	}
	if !m.locs[0].projects[1].starting {
		t.Errorf("the started project should be marked starting")
	}
	if !m.ticking {
		t.Errorf("a bring-up in flight should arm the spinner")
	}

	// The launcher stays usable while it turns, and the tick loop stops once the
	// bring-up reports back (D29).
	m = updLauncher(t, m, launcherStartedMsg{target: fb.StartedProjects[0]})
	if m.locs[0].projects[1].starting || !m.locs[0].projects[1].Running {
		t.Errorf("a finished bring-up should stop spinning and read as running")
	}
	if _, cmd := m.Update(launcherTickMsg{}); cmd != nil {
		t.Errorf("the tick loop should stop when nothing is starting")
	}
}

// TestLauncherStartEnabledKeepsTheBatch is the other half of `s`: an enabled
// project that cannot be brought up says so on its own row (D10) while the
// healthy ones still start. Dropping the batch instead would leave the rows
// already marked starting spinning with no bring-up behind them, and the tick
// loop armed for the rest of the session (D29).
func TestLauncherStartEnabledKeepsTheBatch(t *testing.T) {
	fb := &coretest.FakeBackend{LaunchLocs: []LaunchLocation{{
		Dir: "/home/u/gitrepo/cmdman", RepoName: "cmdman", Branch: "main",
		LastUsed: time.Now(), FromHistory: true,
		Projects: []LaunchProject{
			// The healthy row comes first: it is the one already marked starting
			// when the problem row is reached.
			{Name: "devenv", File: "devenv.yaml", FromHistory: true, HasMux: true},
			{
				Name: "broken", File: "broken.yaml", FromHistory: true, HasMux: true,
				Problem: "decode broken.yaml: bad yaml",
			},
		},
	}}}
	m := newLauncher(core.Options{Backend: fb, Widget: core.WidgetLauncher, Version: "v0.0.0-test"})
	m = updLauncher(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = updLauncher(t, m, launchTargetsLoadedMsg{locs: fb.LaunchLocs})
	m = updLauncher(t, m, coretest.KEnter) // into the left list

	next, cmd := m.Update(coretest.Kr("s"))
	m = next.(launcherModel)
	drainLauncherCmd(t, cmd)

	if len(fb.StartedProjects) != 1 || fb.StartedProjects[0].Project != "devenv" {
		t.Fatalf("s started %v, want the healthy project despite the broken one",
			fb.StartedProjects)
	}
	if m.failedLoc != 0 || m.failedPrj != 1 || !strings.Contains(m.failedMsg, "bad yaml") {
		t.Errorf("the broken project should fail on its own row, got loc %d row %d msg %q",
			m.failedLoc, m.failedPrj, m.failedMsg)
	}
	if m.locs[0].projects[1].starting {
		t.Errorf("a project that cannot be brought up must not be marked starting")
	}
	if !m.locs[0].projects[0].starting || !m.ticking {
		t.Errorf("the healthy project should be spinning with its bring-up in flight")
	}

	// Nothing is left spinning once the one in-flight bring-up reports back: the
	// row marked starting is exactly the row that had a command.
	m = updLauncher(t, m, launcherStartedMsg{target: fb.StartedProjects[0]})
	if m.anyStarting() {
		t.Errorf("a row is still marked starting with no bring-up behind it: %+v",
			m.locs[0].projects)
	}
	if _, cmd := m.Update(launcherTickMsg{}); cmd != nil {
		t.Errorf("the tick loop should stop once nothing is starting")
	}
}

// TestLauncherLanding covers `S` and its three endings (D10): a plain landing
// dismisses the launcher, a mux-less project's warning keeps it open to be read
// (D9), and a launch that failed outright stays inline on its row.
func TestLauncherLanding(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	m = updLauncher(t, m, coretest.KEnter)

	next, cmd := m.Update(coretest.Kr("S"))
	m = next.(launcherModel)
	drainLauncherCmd(t, cmd)
	if len(fb.LaunchedProjects) != 1 || fb.LaunchedProjects[0].Project != "devenv" {
		t.Fatalf("S launched %v, want the location's first enabled project",
			fb.LaunchedProjects)
	}

	// D9: the project is up and focus moved, but the window is a bare shell —
	// that is a notice to read, not an inline failure on the row.
	warned := updLauncher(t, m, launcherLandedMsg{
		target:  fb.LaunchedProjects[0],
		outcome: LaunchOutcome{Warning: "devenv is up, but its compose file has no mux"},
	})
	if warned.quitting {
		t.Errorf("a landing with a warning should keep the launcher open to show it")
	}
	if warned.failedLoc != -1 || warned.failedPrj != -1 {
		t.Errorf("a warned landing is a notice, not an inline failure: %+v", warned.failedMsg)
	}
	if !warned.locs[0].projects[0].Running || warned.locs[0].projects[0].starting {
		t.Errorf("a warned landing still brought the project up")
	}
	if !strings.Contains(warned.note, "no mux") {
		t.Errorf("the warning should be said out loud, got note %q", warned.note)
	}

	// A launch failure lands on its row.
	failed := updLauncher(t, m, launcherLandedMsg{
		target: fb.LaunchedProjects[0],
		err:    errors.New("compose up: boom"),
	})
	if failed.failedLoc != 0 || !strings.Contains(failed.failedMsg, "boom") {
		t.Errorf("a real launch failure should surface on its row, got %+v", failed.failedMsg)
	}

	landed, quit := m.Update(launcherLandedMsg{target: fb.LaunchedProjects[0]})
	if !landed.(launcherModel).quitting || quit == nil {
		t.Errorf("a completed landing should dismiss the launcher")
	}
}

// TestLauncherLandingAttaches covers D8: summoned from outside the multiplexer
// there is no client to switch, so the launcher hands its terminal over and
// dismisses when it comes back.
func TestLauncherLandingAttaches(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	m = updLauncher(t, m, coretest.KEnter)
	next, cmd := m.Update(coretest.Kr("S"))
	m = next.(launcherModel)
	drainLauncherCmd(t, cmd)

	next, cmd = m.Update(launcherLandedMsg{
		target:  fb.LaunchedProjects[0],
		outcome: LaunchOutcome{AttachCommand: []string{"tmux", "attach-session", "-t", "=work"}},
	})
	m = next.(launcherModel)
	if m.quitting {
		t.Errorf("the launcher must stay alive while it holds the attach handoff")
	}
	if cmd == nil {
		t.Fatalf("an attach outcome should run the handoff")
	}

	done, quit := m.Update(launcherAttachedMsg{})
	if !done.(launcherModel).quitting || quit == nil {
		t.Errorf("the launcher should dismiss once the terminal returns")
	}
	if note := updLauncher(t, m, launcherAttachedMsg{err: errors.New("boom")}).note; note == "" {
		t.Errorf("a failed handoff should be said out loud")
	}
}

// TestLauncherStaleEntry is the failure experience (D10/Q12): a project whose
// compose file no longer resolves cannot land, so the reason stays inline with
// the removal offer, and ctrl+d asks the backend to forget it.
func TestLauncherStaleEntry(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	m = typeInto(t, m, "demo")
	m = updLauncher(t, m, coretest.KEnter) // into the left list

	next, _ := m.Update(coretest.Kr("S"))
	m = next.(launcherModel)
	if len(fb.LaunchedProjects) != 0 {
		t.Fatalf("S on a stale entry should not launch, got %v", fb.LaunchedProjects)
	}
	if m.failedLoc < 0 || m.focus != zoneRight {
		t.Fatalf("a stale entry should surface inline and take the cursor")
	}
	if out := m.viewContent(); !strings.Contains(out, "missing: gone.yaml") ||
		!strings.Contains(out, "ctrl+d") {
		t.Errorf("the stale row should spell out the failure and the offer:\n%s", out)
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = next.(launcherModel)
	drainLauncherCmd(t, cmd)
	if len(fb.ForgotTargets) != 1 || fb.ForgotTargets[0].Project != "demo" {
		t.Fatalf("ctrl+d should ask the backend to forget the entry, got %v", fb.ForgotTargets)
	}
	m = updLauncher(t, m, launcherForgotMsg{target: fb.ForgotTargets[0]})
	if len(m.locs[2].projects) != 0 {
		t.Errorf("a forgotten entry should leave the list")
	}
}

// TestLauncherMarkerPrecedence pins D29's marker order: a bring-up in flight
// outranks an unread bell, which outranks the status dot, and an entry that is
// neither running nor starting keeps a blank slot (D31).
func TestLauncherMarkerPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   launcherMarker
		want string
	}{
		{"starting wins", launcherMarker{starting: true, bell: true, running: true},
			launcherSpinnerFrames[0]},
		{"bell over dot", launcherMarker{bell: true, running: true}, core.GlyphBell},
		{"dot when running", launcherMarker{running: true}, core.GlyphHollow},
		{"blank when cold", launcherMarker{}, ""},
		{"blank outranks a bell nothing is ringing", launcherMarker{bell: true}, ""},
	} {
		if got := tc.mk.glyph(0); got != tc.want {
			t.Errorf("%s: glyph = %q, want %q", tc.name, got, tc.want)
		}
	}

	// The spinner turns, and every frame is one cell wide — a marker that changed
	// width would jitter the whole name column as it turns.
	for i, f := range launcherSpinnerFrames {
		if got := (launcherMarker{starting: true}).glyph(i); got != f {
			t.Errorf("frame %d = %q, want %q", i, got, f)
		}
		if got, want := core.GlyphWidth(f), lipgloss.Width(f); got != want {
			t.Errorf("spinner frame %q measures %d, renderer draws %d", f, got, want)
		}
	}
}

// TestLauncherScrollKeepsWindowFull is the viewport regression the mock found: a
// narrowing filter must not strand the window part-way down the list, hiding
// rows above it while blank space sits below.
func TestLauncherScrollKeepsWindowFull(t *testing.T) {
	ones := func(n int) []int {
		c := make([]int, n)
		for i := range c {
			c[i] = 1
		}
		return c
	}
	// Scrolled to the bottom of a 20-row list, then the filter narrows it to 4.
	if got := scrollTo(0, 16, 5, ones(4)); got != 0 {
		t.Errorf("narrowing to a list shorter than the window = offset %d, want 0", got)
	}
	// 8 rows, a 5-row window, cursor on row 5: the window is pulled up as far as
	// the cursor allows rather than left where the longer list had scrolled it.
	if got := scrollTo(5, 16, 5, ones(8)); got != 1 {
		t.Errorf("narrowing to 8 rows with a 5-row window = offset %d, want 1", got)
	}
	// Following the cursor down still scrolls.
	if got := scrollTo(9, 0, 5, ones(20)); got != 5 {
		t.Errorf("cursor below the fold = offset %d, want 5", got)
	}
	// A row two lines tall (the inline failure) counts as two.
	if got := rowWindow([]int{1, 2, 1, 1}, 0, 4); got != 3 {
		t.Errorf("window over a two-line row = %d rows, want 3", got)
	}

	// Driven through the model: filter down to one location, and the left pane
	// must show it rather than blank space.
	m, _ := seedLauncher(t, 100, 6)
	m.leftOff = 2
	m = typeInto(t, m, "webapp")
	if m.leftOff != 0 {
		t.Errorf("narrowing the filter left the window stranded at %d", m.leftOff)
	}
	rows, _ := m.leftPane(50, m.matched())
	if len(rows) == 0 || !strings.Contains(rows[0], "webapp") {
		t.Errorf("the surviving row should be visible, got %q", rows)
	}
}

// TestLauncherFillsItsWindow pins D27: the launcher renders edge to edge, every
// row exactly the window width and never more rows than the window has.
func TestLauncherFillsItsWindow(t *testing.T) {
	const w, h = 80, 14
	m, _ := seedLauncher(t, w, h)
	m = updLauncher(t, m, coretest.KEnter)

	lines := strings.Split(m.viewContent(), "\n")
	if len(lines) > h {
		t.Fatalf("view = %d lines, must fit the %d-row window", len(lines), h)
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got > w {
			t.Errorf("line %d width = %d, must not exceed %d (%q)", i, got, w, l)
		}
	}
	out := m.viewContent()
	for _, want := range []string{"locations", "projects at cmdman(main)", "devenv", "test"} {
		if !strings.Contains(out, want) {
			t.Errorf("launcher view should contain %q:\n%s", want, out)
		}
	}
}

// TestLauncherToggleAndCompletion covers the two remaining right-pane gestures:
// space toggles a project, and tab completes the input toward the common path
// prefix of what it matches.
func TestLauncherToggleAndCompletion(t *testing.T) {
	m, _ := seedLauncher(t, 100, 20)
	m = updLauncher(t, m, coretest.KEnter)
	m = updLauncher(t, m, coretest.KEnter) // into the right list
	if !m.locs[0].projects[0].enabled {
		t.Fatalf("a history project should open enabled (D28)")
	}
	m = updLauncher(t, m, coretest.Kr(" "))
	if m.locs[0].projects[0].enabled {
		t.Errorf("space should toggle the project under the cursor")
	}

	m, _ = seedLauncher(t, 100, 20)
	m = typeInto(t, m, "/home/u/src")
	m = updLauncher(t, m, kTab)
	if m.filter != "/home/u/src/" {
		t.Errorf("tab completed to %q, want the common prefix %q", m.filter, "/home/u/src/")
	}
	// Tab moves down the tree or does nothing: with no prefix past what is
	// already typed it says so instead of widening the search.
	m = updLauncher(t, m, kTab)
	if m.filter != "/home/u/src/" || m.note == "" {
		t.Errorf("a second tab changed the query to %q (note %q)", m.filter, m.note)
	}
}

// TestLauncherMouse covers D31's confirmed mouse model: a click selects a
// location or toggles a project and moves the keyboard to that pane, while the
// wheel scrolls the pane under the pointer without moving the cursor.
func TestLauncherMouse(t *testing.T) {
	m, _ := seedLauncher(t, 100, 20)

	// The second location's row: the panes start at launcherPaneTop.
	m = updLauncher(t, m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 3, Y: launcherPaneTop + 1})
	if m.focus != zoneLeft || m.leftSel != 1 {
		t.Fatalf("clicking a location = zone %d row %d, want the left pane on row 1",
			m.focus, m.leftSel)
	}

	leftW, _ := m.widths()
	enabled := m.locs[1].projects[0].enabled
	m = updLauncher(t, m, tea.MouseClickMsg{
		Button: tea.MouseLeft, X: leftW + 2, Y: launcherPaneTop,
	})
	if m.focus != zoneRight || m.locs[1].projects[0].enabled == enabled {
		t.Fatalf("clicking a project should focus the right pane and toggle it")
	}

	m = updLauncher(t, m, tea.MouseClickMsg{Button: tea.MouseLeft, X: 2, Y: 0})
	if m.focus != zoneInput {
		t.Errorf("clicking the input line = zone %d, want the input", m.focus)
	}

	sel := m.leftSel
	m = updLauncher(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 1, Y: 5})
	if m.leftSel != sel {
		t.Errorf("the wheel should scroll without moving the cursor")
	}
	if m.leftOff == 0 {
		t.Errorf("the wheel should scroll the pane it is over")
	}
}

// TestLauncherColdRowsKeepABlankSlot pins D31's blank marker slot: an entry that
// is neither running nor starting shows nothing there, and its name still lines
// up with the rows that do carry a marker.
func TestLauncherColdRowsKeepABlankSlot(t *testing.T) {
	m, _ := seedLauncher(t, 100, 20)
	rows, _ := m.leftPane(50, m.matched())
	if len(rows) < 2 {
		t.Fatalf("expected the history rows, got %d", len(rows))
	}
	if !strings.Contains(rows[0], core.GlyphHollow) {
		t.Errorf("the location with a running project should carry a marker: %q", rows[0])
	}
	if strings.Contains(rows[1], core.GlyphHollow) {
		t.Errorf("a cold location's marker slot should be blank: %q", rows[1])
	}
	// The name column is measured in cells, not bytes: the marker is a
	// multi-byte glyph and the blank slot is spaces.
	nameColumn := func(row, name string) int {
		plain := stripANSI(row)
		return core.Cells.StringWidth(plain[:strings.Index(plain, name)])
	}
	if got, want := nameColumn(rows[1], "webapp"), nameColumn(rows[0], "cmdman"); got != want {
		t.Errorf("a blank slot starts the name at column %d, a marked row at %d:\n%q\n%q",
			got, want, rows[0], rows[1])
	}
}

// TestLauncherWideRunesFitTheirColumn is the defect a CJK path exposes: the
// space a row leaves for its path is measured in cells, so the tail it keeps has
// to be cut in cells too — cutting by rune count hands back up to twice the
// width that was reserved, which is a row that overruns its pane and, before
// that, a negative pad the renderer used to panic on.
func TestLauncherWideRunesFitTheirColumn(t *testing.T) {
	fb := &coretest.FakeBackend{LaunchLocs: []LaunchLocation{
		{
			Dir: "/home/u/作業場/日本語のディレクトリ名", RepoName: "リポジトリ",
			Branch: "主線", LastUsed: time.Now(), FromHistory: true,
			Projects: []LaunchProject{
				// No mux: section, so the file gets " · no mux" appended before it
				// is cut — the right pane's own truncation.
				{Name: "開発環境", File: "構成ファイル/日本語コンポーズ.yaml", FromHistory: true},
				{Name: "テスト", File: "テスト用の長い名前の構成ファイル.yaml", HasMux: true},
			},
		},
		{
			Dir: "/home/u/src/webapp", RepoName: "webapp", Branch: "main",
			LastUsed: time.Now().Add(-time.Hour), FromHistory: true,
			Projects: []LaunchProject{{Name: "staging", File: "compose.yaml", HasMux: true}},
		},
	}}

	for w := 24; w <= 100; w++ {
		m := newLauncher(
			core.Options{Backend: fb, Widget: core.WidgetLauncher, Version: "v0.0.0-test"},
		)
		m = updLauncher(t, m, tea.WindowSizeMsg{Width: w, Height: 20})
		m = updLauncher(t, m, launchTargetsLoadedMsg{locs: fb.LaunchLocs})
		m = updLauncher(t, m, coretest.KEnter) // into the left list, so the right pane draws

		leftW, rightW := m.widths()
		left, _ := m.leftPane(leftW, m.matched())
		for i, row := range left {
			if got := lipgloss.Width(row); got != leftW {
				t.Fatalf("width %d: left row %d = %d cells, want exactly %d (%q)",
					w, i, got, leftW, row)
			}
		}
		right, _ := m.rightPane(rightW)
		for i, row := range right {
			if got := lipgloss.Width(row); got != rightW {
				t.Fatalf("width %d: right row %d = %d cells, want exactly %d (%q)",
					w, i, got, rightW, row)
			}
		}
		for i, line := range strings.Split(m.viewContent(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width %d: line %d = %d cells, must not exceed the window (%q)",
					w, i, got, line)
			}
		}
	}
}

// TestShortPathKeepsTheTailInCells covers shortPath on its own: the home prefix
// is abbreviated only on a separator boundary, and whatever it keeps fits the
// cells it was given whether the path is ASCII or double-width.
func TestShortPathKeepsTheTailInCells(t *testing.T) {
	for _, p := range []string{
		"/home/u/src/webapp/compose.yaml",
		"/home/u/作業場/日本語のディレクトリ名/構成.yaml",
		"日本語",
		"",
	} {
		for w := range 40 {
			got := shortPath(p, w)
			if core.Cells.StringWidth(got) > w {
				t.Errorf("shortPath(%q, %d) = %q, %d cells wide",
					p, w, got, core.Cells.StringWidth(got))
			}
			if core.Cells.StringWidth(got) != lipgloss.Width(got) {
				t.Errorf("shortPath(%q, %d) = %q measures %d by cells and %d by lipgloss",
					p, w, got, core.Cells.StringWidth(got), lipgloss.Width(got))
			}
		}
	}
}

// TestAbbrevHomeNeedsAComponentBoundary pins the "~" abbreviation to whole path
// components: a directory that merely starts with the home path's letters is not
// under it, and shortening it would name somewhere else entirely.
func TestAbbrevHomeNeedsAComponentBoundary(t *testing.T) {
	const home = "/home/watage"
	for _, tc := range []struct{ path, want string }{
		{"/home/watage/gitrepo", "~/gitrepo"},
		{"/home/watage", "~"},
		{"/home/watage/", "~/"},
		{"/home/watageX/y", "/home/watageX/y"},
		{"/home/watagex", "/home/watagex"},
		{"/var/tmp", "/var/tmp"},
	} {
		if got := abbrevHome(tc.path, home); got != tc.want {
			t.Errorf("abbrevHome(%q, %q) = %q, want %q", tc.path, home, got, tc.want)
		}
	}
	// No home to abbreviate against leaves every path alone.
	if got := abbrevHome("/home/watage/x", ""); got != "/home/watage/x" {
		t.Errorf("abbrevHome with no home = %q, want the path unchanged", got)
	}
}

// drainLauncherCmd runs a returned command (including the members of a batch) so
// the fake backend records the calls the model issued.
func drainLauncherCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drainLauncherCmd(t, c)
		}
	}
}

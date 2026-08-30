package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// launcherFixture is the shape the launcher was designed against: history
// locations in recency order, one of them stale, plus a location that was never
// launched from and therefore shows up only once the filter reaches it.
func launcherFixture() []core.LaunchLocation {
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	return []core.LaunchLocation{
		{
			Dir: "/home/u/gitrepo/cmdman", RepoName: "cmdman",
			RepoURI: "github.com/ngicks/cmdman", Branch: "main",
			LastUsed: base, FromHistory: true,
			Projects: []core.LaunchProject{
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
			Projects: []core.LaunchProject{
				{Name: "staging", File: "compose.yaml", FromHistory: true, HasMux: true},
				{Name: "prod", File: "prod.yaml", HasMux: true},
			},
		},
		{
			Dir: "/home/u/old/demo-stack", LastUsed: base.Add(-2 * time.Hour), FromHistory: true,
			Projects: []core.LaunchProject{
				{
					Name: "demo", File: "gone.yaml", FromHistory: true,
					Problem: "missing: gone.yaml", Missing: true,
				},
			},
		},
		{
			// Never launched from: discovered on disk, so it is not in history.
			Dir: "/home/u/src/blog", RepoName: "blog", Branch: "main",
			Projects: []core.LaunchProject{{Name: "blog", File: "compose.yaml", HasMux: true}},
		},
	}
}

func seedLauncher(t *testing.T, width, height int) (Model, *coretest.FakeBackend) {
	t.Helper()
	fb := &coretest.FakeBackend{LaunchLocs: launcherFixture()}
	m := New(
		context.Background(),
		core.Options{Backend: fb, Widget: core.WidgetLauncher, Version: "v0.0.0-test"},
	)
	m = updLauncher(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	return updLauncher(t, m, launchTargetsLoadedMsg{locs: fb.LaunchLocs}), fb
}

func updLauncher(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return got
}

// typeInto drives literal keystrokes, the way a user produces them.
func typeInto(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m = updLauncher(t, m, coretest.Kr(string(r)))
	}
	return m
}

// leftLabels is what the left pane currently lists, by location label.
func leftLabels(m Model) []string {
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

// launcherConfigFixture is the listing with the compose config dir in it: two
// history locations, two locations that exist only because the config dir names
// them, and one that is merely on disk. The config rows arrive out of name order
// so the opening list has to sort them rather than echo what it was handed.
func launcherConfigFixture() []core.LaunchLocation {
	base := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	return []core.LaunchLocation{
		{
			Dir: "/home/u/gitrepo/cmdman", RepoName: "cmdman", Branch: "main",
			LastUsed: base, FromHistory: true,
			Projects: []core.LaunchProject{
				{Name: "devenv", File: "devenv.yaml", FromHistory: true, HasMux: true},
			},
		},
		{
			Dir: "/home/u/src/webapp", RepoName: "webapp", Branch: "feat/auth",
			LastUsed: base.Add(-time.Hour), FromHistory: true,
			Projects: []core.LaunchProject{
				{Name: "staging", File: "compose.yaml", FromHistory: true, HasMux: true},
			},
		},
		{
			Dir: "/home/u/cfg/zeta", FromConfig: true,
			Projects: []core.LaunchProject{
				{Name: "zeta", File: "zeta.yaml", FromConfig: true, HasMux: true},
			},
		},
		{
			Dir: "/home/u/cfg/alpha", FromConfig: true,
			Projects: []core.LaunchProject{
				{Name: "alpha", File: "alpha.yaml", FromConfig: true, HasMux: true},
			},
		},
		{
			// Neither known from history nor named in the config dir: discovered in
			// the store or in the working directory, so the filter has to reach it.
			Dir: "/home/u/src/blog", RepoName: "blog", Branch: "main",
			Projects: []core.LaunchProject{{Name: "blog", File: "compose.yaml", HasMux: true}},
		},
	}
}

func seedLauncherWith(t *testing.T, locs []core.LaunchLocation) (Model, *coretest.FakeBackend) {
	t.Helper()
	fb := &coretest.FakeBackend{LaunchLocs: locs}
	m := New(
		context.Background(),
		core.Options{Backend: fb, Widget: core.WidgetLauncher, Version: "v0.0.0-test"},
	)
	m = updLauncher(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	return updLauncher(t, m, launchTargetsLoadedMsg{locs: fb.LaunchLocs}), fb
}

// TestLauncherConfigDirOpensWithHistory is the opening list once the compose
// config dir has a say: a project the user keeps around is worth showing before
// it has ever been launched, so the config-dir locations join the history ones
// under the empty input. They come after them and by name — a location never
// launched from has no recency to be placed by — and a location that is merely
// on disk still waits for the filter.
func TestLauncherConfigDirOpensWithHistory(t *testing.T) {
	m, _ := seedLauncherWith(t, launcherConfigFixture())

	want := []string{"cmdman(main)", "webapp(feat/auth)", "alpha", "zeta"}
	if got := leftLabels(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("opening list = %v, want %v", got, want)
	}

	// The config-only location is selectable, and the project under it is what the
	// right pane offers there.
	m = updLauncher(t, m, coretest.KEnter) // into the left list
	for range 2 {
		m = updLauncher(t, m, coretest.Kr("j"))
	}
	li, ok := m.currentLoc()
	if !ok || m.locs[li].Dir != "/home/u/cfg/alpha" {
		t.Fatalf("the cursor should reach the config-dir location, got %d/%v", li, ok)
	}
	if ps := m.locs[li].projects; len(ps) != 1 || ps[0].Name != "alpha" {
		t.Fatalf("projects at the config-dir location = %v, want [alpha]", ps)
	}

	m = updLauncher(t, m, coretest.Kr("/")) // back to the input zone
	m = typeInto(t, m, "blog")
	if got := leftLabels(m); len(got) != 1 || got[0] != "blog(main)" {
		t.Fatalf("filtering to the on-disk location = %v, want [blog(main)]", got)
	}
}

// TestLauncherConfigProjectsStartDisabled is the other half of letting the
// config dir into the opening list: a project that was never run must not join a
// bulk `s` aimed at the ones the user actually works with. It opens disabled and
// space is what puts it in the batch.
func TestLauncherConfigProjectsStartDisabled(t *testing.T) {
	m, fb := seedLauncherWith(t, []core.LaunchLocation{{
		Dir: "/home/u/gitrepo/cmdman", RepoName: "cmdman", Branch: "main",
		LastUsed: time.Now(), FromHistory: true, FromConfig: true,
		Projects: []core.LaunchProject{
			{Name: "devenv", File: "devenv.yaml", FromHistory: true, HasMux: true},
			{Name: "sandbox", File: "sandbox.yaml", FromConfig: true, HasMux: true},
		},
	}})
	if !m.locs[0].projects[0].enabled {
		t.Fatalf("a history project should open enabled")
	}
	if m.locs[0].projects[1].enabled {
		t.Fatalf("a config-dir project that was never run should open disabled")
	}

	m = updLauncher(t, m, coretest.KEnter) // into the left list
	next, cmd := m.Update(coretest.Kr("s"))
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.StartedProjects) != 1 || fb.StartedProjects[0].Project != "devenv" {
		t.Fatalf("s started %v, want only the history project", fb.StartedProjects)
	}

	m = updLauncher(t, m, coretest.KEnter) // into the right list
	m = updLauncher(t, m, coretest.Kr("j"))
	m = updLauncher(t, m, coretest.Kr(" "))
	if !m.locs[0].projects[1].enabled {
		t.Fatalf("space should put the config-dir project in the batch")
	}
	next, cmd = m.Update(coretest.Kr("s"))
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.StartedProjects) != 2 || fb.StartedProjects[1].Project != "sandbox" {
		t.Fatalf("s after the toggle started %v, want sandbox as well", fb.StartedProjects)
	}

	// A location whose only project came from the config dir has nothing enabled
	// at all, so `s` there says so instead of starting anything.
	m, fb = seedLauncherWith(t, []core.LaunchLocation{{
		Dir: "/home/u/cfg/alpha", FromConfig: true,
		Projects: []core.LaunchProject{
			{Name: "alpha", File: "alpha.yaml", FromConfig: true, HasMux: true},
		},
	}})
	m = updLauncher(t, m, coretest.KEnter)
	next, cmd = m.Update(coretest.Kr("s"))
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.StartedProjects) != 0 {
		t.Errorf("s started %v at a config-only location, want nothing", fb.StartedProjects)
	}
	if !strings.Contains(m.note, "nothing enabled") {
		t.Errorf("s with nothing enabled noted %q, want the space hint", m.note)
	}
}

// TestLauncherMatchFields covers D18's match surface: a row survives on its
// branch, its repo uri or name, its full path, or one of its project names — and
// the field that matched is reported, so each is exercised independently. The
// path field takes the query with its home spelling expanded, so a path typed
// the way the pane displays it reaches the row that holds it in full.
func TestLauncherMatchFields(t *testing.T) {
	const home = "/home/u"
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
		{"~/src/webapp", webapp, "path"},
		{"$HOME/gitrepo/cmdman", cmdmanLoc, "path"},
	} {
		field, ok := matchLaunchLocation(tc.filter, expandHome(tc.filter, home), tc.loc)
		if !ok {
			t.Errorf("filter %q should match %s", tc.filter, tc.loc.Dir)
			continue
		}
		if field != tc.field {
			t.Errorf("filter %q matched on %q, want %q", tc.filter, field, tc.field)
		}
	}
	for _, filter := range []string{"zzz", "~X/src/webapp", "$HOMEX/src/webapp"} {
		if _, ok := matchLaunchLocation(filter, expandHome(filter, home), cmdmanLoc); ok {
			t.Errorf("filter %q should match nothing", filter)
		}
	}
}

// TestLauncherHomeSpellingMatches is the same rule driven through the model: the
// left pane writes paths home-abbreviated (core.ShortPath), so typing one back has to
// find the row it came from — under either spelling, and only on a component
// boundary.
func TestLauncherHomeSpellingMatches(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"~/src/webapp", []string{"webapp(feat/auth)"}},
		{"$HOME/src/webapp", []string{"webapp(feat/auth)"}},
		{"~/gitrepo", []string{"cmdman(main)"}},
		{"~X/src/webapp", nil},
	} {
		m, _ := seedLauncher(t, 100, 20)
		m.home = "/home/u"
		m = typeInto(t, m, tc.query)
		if got := leftLabels(m); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("query %q listed %v, want %v", tc.query, got, tc.want)
		}
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
	m = updLauncher(t, m, coretest.KEsc)
	if m.focus != zoneLeft {
		t.Fatalf("esc from the right list = zone %d, want left", m.focus)
	}
	m = updLauncher(t, m, coretest.KEsc)
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
	m = updLauncher(t, m, coretest.KEsc)
	if m.filter != "" || m.focus != zoneInput {
		t.Fatalf("esc in the input should clear the query first, got %q / zone %d",
			m.filter, m.focus)
	}
	next, cmd := m.Update(coretest.KEsc)
	if !next.(Model).quitting || cmd == nil {
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
	m = next.(Model)
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
	fb := &coretest.FakeBackend{LaunchLocs: []core.LaunchLocation{{
		Dir: "/home/u/gitrepo/cmdman", RepoName: "cmdman", Branch: "main",
		LastUsed: time.Now(), FromHistory: true,
		Projects: []core.LaunchProject{
			// The healthy row comes first: it is the one already marked starting
			// when the problem row is reached.
			{Name: "devenv", File: "devenv.yaml", FromHistory: true, HasMux: true},
			{
				Name: "broken", File: "broken.yaml", FromHistory: true, HasMux: true,
				Problem: "decode broken.yaml: bad yaml",
			},
		},
	}}}
	m := New(
		context.Background(),
		core.Options{Backend: fb, Widget: core.WidgetLauncher, Version: "v0.0.0-test"},
	)
	m = updLauncher(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m = updLauncher(t, m, launchTargetsLoadedMsg{locs: fb.LaunchLocs})
	m = updLauncher(t, m, coretest.KEnter) // into the left list

	next, cmd := m.Update(coretest.Kr("s"))
	m = next.(Model)
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
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.LaunchedProjects) != 1 || fb.LaunchedProjects[0].Project != "devenv" {
		t.Fatalf("S launched %v, want the location's first enabled project",
			fb.LaunchedProjects)
	}

	// D9: the project is up and focus moved, but the window is a bare shell —
	// that is a notice to read, not an inline failure on the row.
	warned := updLauncher(t, m, launcherLandedMsg{
		target:  fb.LaunchedProjects[0],
		outcome: core.LaunchOutcome{Warning: "devenv is up, but its compose file has no mux"},
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
	if !landed.(Model).quitting || quit == nil {
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
	m = next.(Model)
	drainLauncherCmd(t, cmd)

	next, cmd = m.Update(launcherLandedMsg{
		target: fb.LaunchedProjects[0],
		outcome: core.LaunchOutcome{
			AttachCommand: []string{"tmux", "attach-session", "-t", "=work"},
		},
	})
	m = next.(Model)
	if m.quitting {
		t.Errorf("the launcher must stay alive while it holds the attach handoff")
	}
	if cmd == nil {
		t.Fatalf("an attach outcome should run the handoff")
	}

	done, quit := m.Update(launcherAttachedMsg{})
	if !done.(Model).quitting || quit == nil {
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
	m = next.(Model)
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
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.ForgotTargets) != 1 || fb.ForgotTargets[0].Project != "demo" {
		t.Fatalf("ctrl+d should ask the backend to forget the entry, got %v", fb.ForgotTargets)
	}
	m = updLauncher(t, m, launcherForgotMsg{target: fb.ForgotTargets[0]})
	if len(m.locs[2].projects) != 0 {
		t.Errorf("a forgotten entry should leave the list")
	}
}

// launcherFooterText is the footer as it is read, styling stripped.
func launcherFooterText(m Model) string {
	return core.StripANSI(strings.Join(m.footerLines(), "\n"))
}

// TestLauncherMuxDown covers `d`: the dashboard of the project `S` would launch
// goes away and its commands stay up, so the key acts on the first press and
// names the project the way a launch names it — file and directory included.
func TestLauncherMuxDown(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	m = updLauncher(t, m, coretest.KEnter) // into the left list

	if hints := launcherFooterText(m); !strings.Contains(hints, "d/D down") {
		t.Errorf("a list should hint at the teardowns: %q", hints)
	}

	next, cmd := m.Update(coretest.Kr("d"))
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("d should issue work")
	}
	m = updLauncher(t, m, cmd())

	want := []coretest.DownCall{{
		Project: "devenv", Path: "devenv.yaml", Workdir: "/home/u/gitrepo/cmdman",
	}}
	if !slices.Equal(fb.MuxDowns, want) {
		t.Fatalf("d tore down %v, want %v", fb.MuxDowns, want)
	}
	if len(fb.ComposeDowns) != 0 {
		t.Fatalf("d must leave the commands alone, compose downs = %v", fb.ComposeDowns)
	}
	if !strings.Contains(m.note, "commands still running") {
		t.Errorf("d should say what it left up, note = %q", m.note)
	}

	// A project whose compose file declares no dashboard is the everyday
	// refusal, and the key stays on offer for it.
	fb.MuxDownErr = errors.New("project \"devenv\" has no mux: section")
	next, cmd = m.Update(coretest.Kr("d"))
	m = updLauncher(t, next.(Model), cmd())
	if !strings.Contains(m.note, "no mux: section") {
		t.Errorf("a refused teardown should be reported, note = %q", m.note)
	}

	// In the input zone every key is text, teardowns included.
	typed, fb2 := seedLauncher(t, 100, 20)
	typed = typeInto(t, typed, "dD")
	if typed.filter != "dD" || len(fb2.MuxDowns) != 0 || len(fb2.ComposeDowns) != 0 {
		t.Errorf("typing d/D left filter %q and tore down %v / %v",
			typed.filter, fb2.MuxDowns, fb2.ComposeDowns)
	}
}

// TestLauncherComposeDownConfirms covers `D`: it takes away every command of
// the project, so the first press only asks. y goes ahead and reports what the
// teardown did; any other key takes the question back and is spent doing so.
func TestLauncherComposeDownConfirms(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	fb.ComposeDownSummary = core.DownSummary{Stopped: 3, Removed: 2}
	m = updLauncher(t, m, coretest.KEnter) // into the left list

	next, cmd := m.Update(coretest.Kr("D"))
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("D on its own should dispatch nothing")
	}
	if len(fb.ComposeDowns) != 0 {
		t.Fatalf("D on its own tore down %v", fb.ComposeDowns)
	}
	if foot := launcherFooterText(m); !strings.Contains(foot, "compose down devenv? y/n") {
		t.Fatalf("D should ask before it acts, footer = %q", foot)
	}

	cancelled, cmd := m.Update(coretest.Kr("j"))
	if cmd != nil || len(fb.ComposeDowns) != 0 {
		t.Fatalf("a key that is not y should tear nothing down, %v", fb.ComposeDowns)
	}
	if got := cancelled.(Model); got.leftSel != 0 {
		t.Errorf("the key that cancelled should not also move the cursor, leftSel = %d",
			got.leftSel)
	} else if !strings.Contains(got.note, "cancelled") {
		t.Errorf("a cancelled teardown should say so, note = %q", got.note)
	}
	// With the question answered the same key is a movement key again.
	if moved := updLauncher(t, cancelled.(Model), coretest.Kr("j")); moved.leftSel != 1 {
		t.Errorf("j after the question should move again, leftSel = %d", moved.leftSel)
	}

	next, cmd = m.Update(coretest.Kr("y"))
	if cmd == nil {
		t.Fatalf("y should issue the teardown")
	}
	m = updLauncher(t, next.(Model), cmd())
	want := []coretest.DownCall{{
		Project: "devenv", Path: "devenv.yaml", Workdir: "/home/u/gitrepo/cmdman",
	}}
	if !slices.Equal(fb.ComposeDowns, want) {
		t.Fatalf("y tore down %v, want %v", fb.ComposeDowns, want)
	}
	if len(fb.MuxDowns) != 0 {
		t.Errorf("D must not touch the dashboard, mux downs = %v", fb.MuxDowns)
	}
	if !strings.Contains(m.note, "stopped 3, removed 2") {
		t.Errorf("the teardown should report what it did, note = %q", m.note)
	}
}

// TestLauncherComposeDownTakesTheProjectUnderTheCursor pins what the two keys
// act on: the project `S` would launch, which on the projects pane is the row
// the cursor is on rather than the location's first enabled one.
func TestLauncherComposeDownTakesTheProjectUnderTheCursor(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	m = updLauncher(t, m, coretest.KEnter) // into the left list
	m = updLauncher(t, m, coretest.KEnter) // into the projects pane
	m = updLauncher(t, m, coretest.Kr("j"))

	next, cmd := m.Update(coretest.Kr("D"))
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("D on its own should dispatch nothing")
	}
	if foot := launcherFooterText(m); !strings.Contains(foot, "compose down test? y/n") {
		t.Fatalf("D should ask about the row under the cursor, footer = %q", foot)
	}
	next, cmd = m.Update(coretest.Kr("y"))
	updLauncher(t, next.(Model), cmd())
	want := []coretest.DownCall{{
		Project: "test", Path: "test.yaml", Workdir: "/home/u/gitrepo/cmdman",
	}}
	if !slices.Equal(fb.ComposeDowns, want) {
		t.Fatalf("y tore down %v, want %v", fb.ComposeDowns, want)
	}
}

// TestLauncherComposeDownReportsPartialTeardown pins the half a non-nil error
// must not hide: a teardown that failed on one command still tore the others
// down, and both belong on the note line. The question also outlives a bring-up
// reporting back under it, since the next key still answers it.
func TestLauncherComposeDownReportsPartialTeardown(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	fb.ComposeDownSummary = core.DownSummary{Stopped: 2, Removed: 1}
	fb.ComposeDownErr = errors.New("remove watcher: still running")
	m = updLauncher(t, m, coretest.KEnter)

	next, _ := m.Update(coretest.Kr("D"))
	m = next.(Model)
	m = updLauncher(t, m, launcherStartedMsg{target: core.LaunchTarget{
		WorkDir: "/home/u/src/webapp", Project: "staging",
	}})
	if foot := launcherFooterText(m); !strings.Contains(foot, "compose down devenv? y/n") {
		t.Fatalf("the question should outlive a reply landing under it, footer = %q", foot)
	}

	next, cmd := m.Update(coretest.Kr("y"))
	m = updLauncher(t, next.(Model), cmd())
	if !strings.Contains(m.note, "stopped 2, removed 1") ||
		!strings.Contains(m.note, "remove watcher: still running") {
		t.Errorf("a partial teardown should report both halves, note = %q", m.note)
	}
}

// TestLauncherComposeDownThenStart pins what a teardown leaves behind: the row
// it took down no longer reads as running, so the `s` that follows brings the
// project back up instead of skipping it as already up (D31).
func TestLauncherComposeDownThenStart(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	m = updLauncher(t, m, coretest.KEnter) // into the left list

	next, _ := m.Update(coretest.Kr("D"))
	next, cmd := next.(Model).Update(coretest.Kr("y"))
	m = updLauncher(t, next.(Model), cmd())
	if m.locs[0].projects[0].Running || m.locs[0].projects[0].starting {
		t.Fatalf("a finished teardown should leave the row idle, got %+v",
			m.locs[0].projects[0])
	}

	next, cmd = m.Update(coretest.Kr("s"))
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.StartedProjects) != 1 || fb.StartedProjects[0].Project != "devenv" {
		t.Fatalf("s after a teardown started %v, want the project that was taken down",
			fb.StartedProjects)
	}
	if strings.Contains(m.note, "already up here") {
		t.Errorf("s must not refuse the project it just tore down, note = %q", m.note)
	}
}

// TestLauncherMuxDownThenStart is the same for `d`: the dashboard is gone, so
// `s` is what builds it again — the bring-up finds the commands already up and
// rebuilds the windows around them.
func TestLauncherMuxDownThenStart(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	m = updLauncher(t, m, coretest.KEnter) // into the left list

	next, cmd := m.Update(coretest.Kr("d"))
	m = updLauncher(t, next.(Model), cmd())
	if m.locs[0].projects[0].Running || m.locs[0].projects[0].starting {
		t.Fatalf("a finished dashboard teardown should leave the row idle, got %+v",
			m.locs[0].projects[0])
	}

	next, cmd = m.Update(coretest.Kr("s"))
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.StartedProjects) != 1 || fb.StartedProjects[0].Project != "devenv" {
		t.Fatalf("s after a dashboard teardown started %v, want the project it tore down",
			fb.StartedProjects)
	}
	if strings.Contains(m.note, "already up here") {
		t.Errorf("s must not refuse a project whose dashboard is gone, note = %q", m.note)
	}
}

// TestLauncherFailedDownKeepsTheRow is the other half of both: a teardown that
// came back with an error took nothing down that the launcher can count on, so
// the row keeps what it said and `s` still skips it.
func TestLauncherFailedDownKeepsTheRow(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	fb.MuxDownErr = errors.New(`project "devenv" has no mux: section`)
	fb.ComposeDownErr = errors.New("remove watcher: still running")
	m = updLauncher(t, m, coretest.KEnter) // into the left list

	next, cmd := m.Update(coretest.Kr("d"))
	m = updLauncher(t, next.(Model), cmd())
	if !m.locs[0].projects[0].Running {
		t.Fatalf("a refused dashboard teardown should leave the row running")
	}

	next, _ = m.Update(coretest.Kr("D"))
	next, cmd = next.(Model).Update(coretest.Kr("y"))
	m = updLauncher(t, next.(Model), cmd())
	if !m.locs[0].projects[0].Running {
		t.Fatalf("a failed teardown should leave the row running")
	}

	next, cmd = m.Update(coretest.Kr("s"))
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.StartedProjects) != 0 {
		t.Fatalf("s should still skip the row nothing took down, started %v",
			fb.StartedProjects)
	}
	if !strings.Contains(m.note, "already up here") {
		t.Errorf("s with nothing left to start should say so, note = %q", m.note)
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
	m = updLauncher(t, m, coretest.KTab)
	if m.filter != "/home/u/src/" {
		t.Errorf("tab completed to %q, want the common prefix %q", m.filter, "/home/u/src/")
	}
	// Two locations extend it, so the prefix is as far as tab can decide: the
	// list opens behind it and the next tab chooses between them (D43).
	if !m.menuOpen() || m.cycling() {
		t.Errorf("an ambiguous completion should list the candidates without choosing, got %v",
			m.menu)
	}
	m = updLauncher(t, m, coretest.KTab)
	if m.filter != "/home/u/src/blog" {
		t.Errorf("a second tab put %q in the input, want the first candidate", m.filter)
	}

	// Tab moves down the tree or does nothing: with no prefix past what is
	// already typed and nothing to choose between, it says so instead of
	// widening the search.
	m, _ = seedLauncher(t, 100, 20)
	m = updLauncher(t, typeInto(t, m, "/home/u/gitrepo/cmdman"), coretest.KTab)
	if m.filter != "/home/u/gitrepo/cmdman" || m.note == "" || m.menuOpen() {
		t.Errorf("tab on the one location it matches changed the query to %q (note %q)",
			m.filter, m.note)
	}

	// A query that is not a path completes over the listing alone and gains no
	// separator: only a path being typed is one the filesystem has a say in. The
	// row's directory is on disk, so the separator is exactly what the completion
	// would append were the input's shape not what decides it.
	dir := filepath.Join(t.TempDir(), "webapp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir webapp: %v", err)
	}
	m, _ = seedLauncher(t, 100, 20)
	m = updLauncher(t, m, launchTargetsLoadedMsg{locs: []core.LaunchLocation{{
		Dir: dir, RepoName: "webapp", Branch: "main", FromHistory: true,
		Projects: []core.LaunchProject{{Name: "staging", File: "compose.yaml"}},
	}}})
	m = typeInto(t, m, "webapp")
	m = updLauncher(t, m, coretest.KTab)
	if m.filter != dir {
		t.Errorf("tab over a bare word completed to %q, want %q", m.filter, dir)
	}
}

// TestLauncherCompletesPathsOnDisk covers D28's tab completion where the paths
// actually are: a path being typed completes over the directories under it, not
// only over the locations the listing happens to know.
func TestLauncherCompletesPathsOnDisk(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"proj-alpha/inner", "proj-beta", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "proj-gamma"), nil, 0o600); err != nil {
		t.Fatalf("write proj-gamma: %v", err)
	}

	tab := func(t *testing.T, query string) Model {
		t.Helper()
		m, _ := seedLauncher(t, 100, 20)
		return updLauncher(t, typeInto(t, m, query), coretest.KTab)
	}

	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		// Two directories share the prefix, so the completion stops between them.
		{"common prefix", root + "/proj", root + "/proj-"},
		// One directory left: the separator is added so the next tab reaches in.
		{"unique directory", root + "/proj-a", root + "/proj-alpha/"},
		{"inside it", root + "/proj-alpha/", root + "/proj-alpha/inner/"},
		// A regular file is not a location, and a dot-directory stays out of the
		// way until the user types the dot.
		{"a file is not a location", root + "/proj-g", root + "/proj-g"},
		{"dot directory when asked for", root + "/.", root + "/.hidden/"},
	} {
		if got := tab(t, tc.query).filter; got != tc.want {
			t.Errorf("%s: tab on %q = %q, want %q", tc.name, tc.query, got, tc.want)
		}
	}

	// A completed prefix that is itself a directory keeps its siblings reachable:
	// the separator would cut proj-alpha-2 out of the search. With nothing left to
	// extend, tab offers the two instead — and choosing one *does* take the
	// separator, since that is what choosing it means (D43).
	if err := os.MkdirAll(filepath.Join(root, "proj-alpha-2"), 0o755); err != nil {
		t.Fatalf("mkdir proj-alpha-2: %v", err)
	}
	m := tab(t, root+"/proj-alpha")
	if m.filter != root+"/proj-alpha/" || !m.cycling() {
		t.Errorf("tab on a directory with a sibling = %q, want the first candidate chosen",
			m.filter)
	}
	if got := updLauncher(t, m, coretest.KTab).filter; got != root+"/proj-alpha-2/" {
		t.Errorf("the next tab = %q, want the sibling the separator would have cut out", got)
	}

	// A relative path is a path too, resolved against the process cwd and written
	// back the way it was typed.
	t.Chdir(root)
	if got := tab(t, "./proj-b").filter; got != "./proj-beta/" {
		t.Errorf("tab on %q = %q, want %q", "./proj-b", got, "./proj-beta/")
	}
}

// TestLauncherCompletionStaysValidUTF8 pins commonPrefix's trim: names off the
// filesystem reach it, and two siblings can share the lead bytes of a multibyte
// character without sharing the character — a completion stopping between them
// must not put half a rune under the cursor.
func TestLauncherCompletionStaysValidUTF8(t *testing.T) {
	if got := commonPrefix([]string{"/x/日本", "/x/日月"}); got != "/x/日" ||
		!utf8.ValidString(got) {
		t.Errorf("commonPrefix over multibyte siblings = %q, want %q", got, "/x/日")
	}

	root := t.TempDir()
	for _, d := range []string{"日本", "日月"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	m, _ := seedLauncher(t, 100, 20)
	m = updLauncher(t, typeInto(t, m, root+"/"), coretest.KTab)
	if want := root + "/日"; m.filter != want || !utf8.ValidString(m.filter) {
		t.Errorf("tab over multibyte siblings = %q, want %q", m.filter, want)
	}
}

// TestLauncherKeepsTheHomeSpelling pins the write-back: the pane shows paths
// abbreviated, so they get typed that way, and a completion must not expand the
// query under the cursor.
func TestLauncherKeepsTheHomeSpelling(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "sandbox", "deep"), 0o755); err != nil {
		t.Fatalf("mkdir sandbox/deep: %v", err)
	}
	for _, tok := range []string{"~", "$HOME"} {
		m, _ := seedLauncher(t, 100, 20)
		m.home = home
		m = updLauncher(t, typeInto(t, m, tok+"/sand"), coretest.KTab)
		if want := tok + "/sandbox/"; m.filter != want {
			t.Errorf("tab on %q completed to %q, want %q", tok+"/sand", m.filter, want)
		}
		m = updLauncher(t, m, coretest.KTab)
		if want := tok + "/sandbox/deep/"; m.filter != want {
			t.Errorf("a second tab completed to %q, want %q", m.filter, want)
		}
	}
}

// kShiftTab cycles the suggestion list backward: terminals send it as tab with
// the shift modifier, which is what bubbletea spells "shift+tab".
var kShiftTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

// menuDirs makes a fresh root holding the siblings tab cannot choose between.
func menuDirs(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", n, err)
		}
	}
	return root
}

// menuFixture is a directory of siblings tab cannot choose between, with the
// launcher parked on the ambiguous prefix — one tab away from the menu.
func menuFixture(t *testing.T, names ...string) (string, Model) {
	t.Helper()
	root := menuDirs(t, names...)
	m, _ := seedLauncher(t, 100, 20)
	return root, typeInto(t, m, root+"/")
}

// seedLauncherAt lists locations at the directories a test made itself, so a
// path typed at their root reaches rows the panes can really draw. The
// suggestion list only ever opens over a typed path, and the listing the other
// tests share lives somewhere that path can never name.
func seedLauncherAt(
	t *testing.T, width, height int, dirs ...string,
) (Model, *coretest.FakeBackend) {
	t.Helper()
	locs := make([]core.LaunchLocation, 0, len(dirs))
	for i, d := range dirs {
		name := filepath.Base(d)
		locs = append(locs, core.LaunchLocation{
			Dir: d, RepoName: name, Branch: "main",
			LastUsed: time.Now().Add(-time.Duration(i) * time.Hour), FromHistory: true,
			Projects: []core.LaunchProject{
				{Name: name, File: "compose.yaml", FromHistory: true, HasMux: true},
			},
		})
	}
	fb := &coretest.FakeBackend{LaunchLocs: locs}
	m := New(
		context.Background(),
		core.Options{Backend: fb, Widget: core.WidgetLauncher, Version: "v0.0.0-test"},
	)
	m = updLauncher(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	return updLauncher(t, m, launchTargetsLoadedMsg{locs: fb.LaunchLocs}), fb
}

// TestLauncherCompletionMenuLists covers D43's list: a tab that extends as far
// as the candidates agree and still cannot decide shows them under the input,
// by last component, capped to a few rows with a count of the rest — and the
// rows come out of the panes' budget rather than off the bottom (D27).
func TestLauncherCompletionMenuLists(t *testing.T) {
	root := t.TempDir()
	for i := range 14 {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("proj-%02d", i)), 0o755); err != nil {
			t.Fatalf("mkdir proj-%02d: %v", i, err)
		}
	}
	// A narrow window, so the grid is two columns and 14 candidates run past the
	// cap without needing 70 directories to do it.
	const w, h = 24, 20
	m, _ := seedLauncher(t, w, h)
	m = updLauncher(t, typeInto(t, m, root+"/proj"), coretest.KTab)

	if m.filter != root+"/proj-" {
		t.Fatalf("tab completed to %q, want the common prefix %q", m.filter, root+"/proj-")
	}
	if !m.menuOpen() || m.cycling() {
		t.Fatalf("an ambiguous completion should list the candidates without choosing one")
	}
	if got := m.menu.labels; len(got) != 14 || got[0] != "proj-00" {
		t.Fatalf("the list shows %v, want every candidate by its last component", got)
	}

	lines := m.menuLines()
	if len(lines) != launcherMenuRows {
		t.Fatalf("the list is %d lines, want it capped at %d", len(lines), launcherMenuRows)
	}
	if got := core.StripANSI(lines[0]); !strings.Contains(got, "proj-00") ||
		!strings.Contains(got, "proj-01") {
		t.Errorf("the first row = %q, want two candidates side by side", got)
	}
	// Five rows of two, and a count of the four that left out.
	if got := core.StripANSI(lines[len(lines)-1]); !strings.Contains(got, "+4 more") {
		t.Errorf("the capped list's tail = %q, want the count of the rest", got)
	}
	if got, want := m.paneTop(), launcherPaneTop+launcherMenuRows; got != want {
		t.Errorf("the panes start at line %d, want %d — the list shrinks them", got, want)
	}

	out := strings.Split(m.viewContent(), "\n")
	if len(out) > h {
		t.Errorf("view = %d lines, must fit the %d-row window", len(out), h)
	}
	for i, l := range out {
		if got := lipgloss.Width(l); got > w {
			t.Errorf("line %d width = %d, must not exceed %d (%q)", i, got, w, l)
		}
	}
	if !strings.Contains(m.viewContent(), "proj-00") {
		t.Errorf("the list should be on screen:\n%s", m.viewContent())
	}

	// A window with no room to spare keeps the launcher whole: the panes and the
	// footer come first, so the list gives up its rows entirely rather than
	// pushing either off the bottom (D27).
	const shortH = 7
	short, _ := seedLauncher(t, 80, shortH)
	short = updLauncher(t, typeInto(t, short, root+"/proj"), coretest.KTab)
	if !short.menuOpen() {
		t.Fatalf("the list is still open in a short window, only undrawable")
	}
	if got := short.paneTop(); got != launcherPaneTop {
		t.Errorf("a list with no room left the panes at %d, want %d", got, launcherPaneTop)
	}
	view := short.viewContent()
	if got := len(strings.Split(view, "\n")); got > shortH {
		t.Errorf("view = %d lines, must fit the %d-row window:\n%s", got, shortH, view)
	}
	if !strings.Contains(view, "esc cancel") || !strings.Contains(view, "locations") {
		t.Errorf("the footer and the panes must survive a window too short:\n%s", view)
	}
}

// TestLauncherCompletionMenuCycles covers D43's menu mode: with nothing left to
// extend, tab puts the candidates in the input one at a time, shift+tab walks
// back, both wrap — and every insertion keeps the home spelling and the
// trailing separator the next tab needs (D42's write-back rule).
func TestLauncherCompletionMenuCycles(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(home, "sandbox", d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for _, tok := range []string{"~", "$HOME"} {
		m, _ := seedLauncher(t, 100, 20)
		m.home = home
		m = updLauncher(t, typeInto(t, m, tok+"/sandbox/"), coretest.KTab)

		if !m.cycling() {
			t.Fatalf("%s: nothing left to extend should start choosing", tok)
		}
		if got := strings.Join(m.menu.labels, ","); got != "alpha,beta,gamma" {
			t.Fatalf("%s: the list shows %q", tok, got)
		}
		if want := tok + "/sandbox/alpha/"; m.filter != want {
			t.Fatalf("%s: the opening tab = %q, want %q", tok, m.filter, want)
		}
		for _, step := range []struct {
			key  tea.KeyPressMsg
			want string
		}{
			{coretest.KTab, "beta"},
			{coretest.KTab, "gamma"},
			{coretest.KTab, "alpha"}, // wraps forward
			{kShiftTab, "gamma"},     // and backward out of the first
			{kShiftTab, "beta"},
		} {
			m = updLauncher(t, m, step.key)
			if want := tok + "/sandbox/" + step.want + "/"; m.filter != want {
				t.Errorf("%s: cycling put %q in the input, want %q", tok, m.filter, want)
			}
		}

		// The block moves with the insertion: the same candidates, drawn
		// differently. Pinned as "rendered differently, reads the same" rather
		// than against the escapes lipgloss happens to emit.
		before, after := m.menuLines(), updLauncher(t, m, coretest.KTab).menuLines()
		if len(before) != len(after) {
			t.Fatalf("%s: cycling changed the list's shape: %d lines then %d",
				tok, len(before), len(after))
		}
		same, reads := true, true
		for i := range before {
			if before[i] != after[i] {
				same = false
			}
			if core.StripANSI(before[i]) != core.StripANSI(after[i]) {
				reads = false
			}
		}
		if same {
			t.Errorf("%s: the list draws the same before and after cycling: %q", tok, after)
		}
		if !reads {
			t.Errorf("%s: cycling rewrote the list itself: %q then %q", tok, before, after)
		}
	}
}

// TestLauncherCompletionMenuDismissal covers D43's four ways out: esc takes the
// insertion back and is spent doing it, enter accepts where it stands without
// spending the zone step too, editing keeps the insertion while dropping the
// list, and ctrl+u erases the query the list was computed for, list included.
func TestLauncherCompletionMenuDismissal(t *testing.T) {
	root, m := menuFixture(t, "alpha", "beta")
	m = updLauncher(t, m, coretest.KTab)
	if m.filter != root+"/alpha/" {
		t.Fatalf("tab = %q, want the first candidate", m.filter)
	}

	esc := updLauncher(t, m, coretest.KEsc)
	if esc.filter != root+"/" || esc.menuOpen() || esc.focus != zoneInput {
		t.Errorf("esc = %q / zone %d, want the query the list opened on", esc.filter, esc.focus)
	}
	// Spent on the list, not on the chain behind it: the *next* esc clears (D31).
	if got := updLauncher(t, esc, coretest.KEsc).filter; got != "" {
		t.Errorf("the esc after the list left %q, want the query cleared", got)
	}

	ent := updLauncher(t, m, coretest.KEnter)
	if ent.menuOpen() || ent.filter != root+"/alpha/" || ent.focus != zoneInput {
		t.Errorf("enter = %q / zone %d, want the insertion accepted in place",
			ent.filter, ent.focus)
	}
	if got := updLauncher(t, ent, coretest.KEnter).focus; got != zoneLeft {
		t.Errorf("the enter after the list = zone %d, want the locations", got)
	}

	if typed := typeInto(t, m, "x"); typed.menuOpen() || typed.filter != root+"/alpha/x" {
		t.Errorf("typing = %q, want the insertion kept and the list gone", typed.filter)
	}
	back := updLauncher(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if back.menuOpen() || back.filter != root+"/alpha" {
		t.Errorf("backspace = %q, want the insertion edited and the list gone", back.filter)
	}

	erased := updLauncher(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if erased.menuOpen() || erased.filter != "" || erased.focus != zoneInput {
		t.Errorf("ctrl+u = %q / zone %d with the list %v, want the query and the list gone",
			erased.filter, erased.focus, erased.menu.labels)
	}
	if got := erased.paneTop(); got != launcherPaneTop {
		t.Errorf("the panes start at %d after ctrl+u, want the list's rows back at %d",
			got, launcherPaneTop)
	}
}

// TestLauncherCompletionMenuIsPathShapedOnly is the guard D42 drew and D43 keeps:
// `src` matches two locations, so its completion is ambiguous in exactly the way
// that opens the list for a typed path — but a bare word is a search over the
// listing, not a path, and gets no list and no cycling.
func TestLauncherCompletionMenuIsPathShapedOnly(t *testing.T) {
	m, _ := seedLauncher(t, 100, 20)
	m = updLauncher(t, typeInto(t, m, "src"), coretest.KTab)

	if m.filter != "/home/u/src/" {
		t.Fatalf("tab over a bare word completed to %q, want the locations' common prefix",
			m.filter)
	}
	if m.menuOpen() || len(m.menuLines()) != 0 {
		t.Errorf("a bare word must not open the suggestion list, got %v", m.menu.labels)
	}
	if got := m.paneTop(); got != launcherPaneTop {
		t.Errorf("with no list the panes start at %d, want %d", got, launcherPaneTop)
	}
}

// TestLauncherCompletionMenuResolvesTheInsertion is the other half of cycling: an
// insertion is an edit to the query like any other, so the debounced lookup that
// turns a typed directory into a row runs for it too (D28/D42).
func TestLauncherCompletionMenuResolvesTheInsertion(t *testing.T) {
	root, m := menuFixture(t, "alpha", "beta")
	dir := filepath.Join(root, "alpha")
	fb := &coretest.FakeBackend{
		LaunchLocs:    launcherFixture(),
		ResolveDirLoc: core.LaunchLocation{Dir: dir, RepoName: "alpha", Branch: "main"},
	}
	m.backend = fb

	next, cmd := m.Update(coretest.KTab)
	if cmd == nil {
		t.Fatalf("cycling edits the query, so it must arm the debounce")
	}
	m = next.(Model)
	if m.filter != dir+"/" {
		t.Fatalf("tab = %q, want the first candidate", m.filter)
	}

	next, cmd = m.Update(core.ReloadTickMsg{Gen: m.resolveGen})
	if cmd == nil {
		t.Fatalf("the inserted directory should be resolved")
	}
	m = updLauncher(t, next.(Model), cmd())
	if len(fb.ResolveDirReq) != 1 || fb.ResolveDirReq[0] != dir {
		t.Fatalf("ResolveLaunchDir asked for %v, want [%s]", fb.ResolveDirReq, dir)
	}
	if got := leftLabels(m); len(got) != 1 || got[0] != "alpha(main)" {
		t.Errorf("the chosen directory should be selectable, got %v", got)
	}
}

// TestLauncherCompletionMenuBeforeAChoice covers the list as it opens, with
// nothing in the input yet to accept: enter is the zone step it has always been
// and takes the list with it, shift+tab starts at the end of the candidates
// rather than nowhere, and the footer offers an acceptance only once there is
// something to accept (D43).
func TestLauncherCompletionMenuBeforeAChoice(t *testing.T) {
	root := menuDirs(t, "proj-a", "proj-b")
	m, _ := seedLauncher(t, 100, 20)
	m = updLauncher(t, typeInto(t, m, root+"/proj"), coretest.KTab)

	if m.filter != root+"/proj-" {
		t.Fatalf("tab completed to %q, want the common prefix %q", m.filter, root+"/proj-")
	}
	if !m.menuOpen() || m.cycling() {
		t.Fatalf("an ambiguous completion should list the candidates without choosing one")
	}

	ent := updLauncher(t, m, coretest.KEnter)
	if ent.focus != zoneLeft || ent.menuOpen() || ent.filter != root+"/proj-" {
		t.Errorf("enter with nothing chosen = zone %d / %q with the list %v, want the "+
			"locations and no list", ent.focus, ent.filter, ent.menu.labels)
	}

	back := updLauncher(t, m, kShiftTab)
	if want := root + "/proj-b/"; !back.cycling() || back.filter != want {
		t.Errorf("shift+tab with nothing chosen = %q, want the last candidate %q",
			back.filter, want)
	}

	listed := core.StripANSI(strings.Join(m.footerLines(), "\n"))
	if strings.Contains(listed, "enter accept") {
		t.Errorf("the footer offers an acceptance with nothing chosen: %q", listed)
	}
	if got := core.StripANSI(strings.Join(back.footerLines(), "\n")); !strings.Contains(
		got, "enter accept",
	) {
		t.Errorf("the footer while cycling = %q, want the acceptance enter really makes", got)
	}
}

// TestLauncherCompletionMenuClosesWhenAFailureTakesTheCursor pins the zone the
// list lives in (D43): it is the input zone's affordance, and a failed reply is
// the one focus change the launcher makes on its own (D10). Left open behind
// that move, the right pane's enter would dismiss the list instead of toggling
// the project, and its esc would spend a zone step rewriting the query.
func TestLauncherCompletionMenuClosesWhenAFailureTakesTheCursor(t *testing.T) {
	root := menuDirs(t, "alpha", "beta")
	dir := filepath.Join(root, "alpha")
	m, fb := seedLauncherAt(t, 100, 20, dir)

	// A bring-up in flight, then back to the input to type a path: the reply is
	// free to land at any point after it, this one included.
	m = updLauncher(t, m, coretest.KEnter) // into the left list
	next, cmd := m.Update(coretest.Kr("s"))
	m = next.(Model)
	drainLauncherCmd(t, cmd)
	if len(fb.StartedProjects) != 1 {
		t.Fatalf("s started %v, want the location's one enabled project", fb.StartedProjects)
	}
	m = updLauncher(t, m, coretest.Kr("/")) // back to the input
	m = updLauncher(t, typeInto(t, m, root+"/"), coretest.KTab)
	if !m.cycling() || m.filter != dir+"/" {
		t.Fatalf("tab = %q with cycling %v, want the first candidate in the input",
			m.filter, m.cycling())
	}

	m = updLauncher(t, m, launcherStartedMsg{
		target: fb.StartedProjects[0], err: errors.New("compose up: boom"),
	})
	if m.focus != zoneRight || !strings.Contains(m.failedMsg, "boom") {
		t.Fatalf("a failure should take the cursor to its row, got zone %d msg %q",
			m.focus, m.failedMsg)
	}
	if m.menuOpen() {
		t.Fatalf("the list must not outlive the input zone, got %v", m.menu.labels)
	}

	// The keys the right pane promises, not the list's: enter toggles the project
	// and esc steps a zone back, both leaving the query where it is.
	enabled := m.locs[0].projects[0].enabled
	m = updLauncher(t, m, coretest.KEnter)
	if m.locs[0].projects[0].enabled == enabled || m.focus != zoneRight {
		t.Errorf("enter on the failed row = zone %d, want the project toggled", m.focus)
	}
	m = updLauncher(t, m, coretest.KEsc)
	if m.focus != zoneLeft || m.filter != dir+"/" {
		t.Errorf("esc on the failed row = zone %d / %q, want the locations with the query kept",
			m.focus, m.filter)
	}
}

// TestLauncherClicksUnderTheCompletionMenu is the offset the list introduces:
// the panes draw from paneTop, so a click is resolved against the rows as they
// were drawn rather than against the no-list baseline (D43).
func TestLauncherClicksUnderTheCompletionMenu(t *testing.T) {
	root := menuDirs(t, "proj-a", "proj-b")
	m, _ := seedLauncherAt(t, 100, 20,
		filepath.Join(root, "proj-a"), filepath.Join(root, "proj-b"))
	m = updLauncher(t, typeInto(t, m, root+"/proj"), coretest.KTab)
	if !m.menuOpen() || len(leftLabels(m)) != 2 {
		t.Fatalf("expected both locations under an open list, got %v", leftLabels(m))
	}
	if top := m.paneTop(); top <= launcherPaneTop {
		t.Fatalf("the list should have pushed the panes past %d, got %d", launcherPaneTop, top)
	}

	left := updLauncher(t, m, tea.MouseClickMsg{
		Button: tea.MouseLeft, X: 3, Y: m.paneTop() + 1,
	})
	if left.focus != zoneLeft || left.leftSel != 1 || left.menuOpen() {
		t.Errorf("clicking the second location under the list = zone %d row %d, want the "+
			"left pane on row 1", left.focus, left.leftSel)
	}

	leftW, _ := m.widths()
	enabled := m.locs[0].projects[0].enabled
	right := updLauncher(t, m, tea.MouseClickMsg{
		Button: tea.MouseLeft, X: leftW + 2, Y: m.paneTop(),
	})
	if right.focus != zoneRight || right.rightSel != 0 || right.menuOpen() ||
		right.locs[0].projects[0].enabled == enabled {
		t.Errorf("clicking a project under the list = zone %d row %d, want the first "+
			"project toggled", right.focus, right.rightSel)
	}
}

// TestLauncherResolvesATypedDirectory covers D28's other half of "type a path":
// a directory the listing never mentioned becomes a row of its own, resolved by
// the backend once the typing settles — and a reply the input has moved past is
// dropped rather than shown against a query that no longer names it.
func TestLauncherResolvesATypedDirectory(t *testing.T) {
	dir := t.TempDir()
	m, fb := seedLauncher(t, 100, 20)
	fb.ResolveDirLoc = core.LaunchLocation{
		Dir: dir, RepoName: "scratch", Branch: "main",
		// A resolve carries whatever history the directory has; the row it becomes
		// still must not join the empty query's history list (D28).
		FromHistory: true,
		Projects:    []core.LaunchProject{{Name: "scratch", File: "compose.yaml", HasMux: true}},
	}
	m = typeInto(t, m, dir)

	// A tick the next keystroke already superseded never reaches the backend.
	if _, cmd := m.Update(core.ReloadTickMsg{Gen: m.resolveGen - 1}); cmd != nil {
		t.Fatalf("a superseded debounce tick should not resolve anything")
	}
	pending, cmd := m.Update(core.ReloadTickMsg{Gen: m.resolveGen})
	if cmd == nil {
		t.Fatalf("a typed directory should be resolved")
	}
	reply := cmd()
	if len(fb.ResolveDirReq) != 1 || fb.ResolveDirReq[0] != dir {
		t.Fatalf("ResolveLaunchDir asked for %v, want [%s]", fb.ResolveDirReq, dir)
	}

	stale := updLauncher(t, typeInto(t, pending.(Model), "X"), reply)
	if len(stale.locs) != len(m.locs) {
		t.Errorf("a reply for a path the input has moved past should be dropped")
	}

	m = updLauncher(t, pending.(Model), reply)
	if got := leftLabels(m); len(got) != 1 || got[0] != "scratch(main)" {
		t.Fatalf("the typed directory should be selectable, got %v", got)
	}
	if m.locs[len(m.locs)-1].FromHistory {
		t.Errorf("a typed location must not join the empty query's history list")
	}
	if got := leftLabels(updLauncher(t, m, coretest.KEsc)); len(got) != 3 {
		t.Errorf("clearing the query should be back to history alone, got %v", got)
	}

	// The listing's row wins: a resolve is thinner than what ListLaunchTargets
	// builds, so a directory the list already carries is neither re-resolved nor
	// added twice.
	if again := updLauncher(t, m, reply); len(again.locs) != len(m.locs) {
		t.Errorf("a directory the list already carries should not be added twice")
	}
	if _, cmd := m.Update(core.ReloadTickMsg{Gen: m.resolveGen}); cmd != nil {
		t.Errorf("a directory the list already carries should not be resolved again")
	}

	failed := updLauncher(t, pending.(Model), launcherResolvedMsg{
		dir: dir, err: errors.New("stat: boom"),
	})
	if !strings.Contains(failed.note, "boom") {
		t.Errorf("a failed resolve should be said out loud, got note %q", failed.note)
	}
}

// TestLauncherResolvesACompletedPath is the trailing-separator regression: tab
// completing onto a directory leaves the separator in the query, while the row
// the backend resolves holds its directory canonically (core.LaunchTarget). The
// two have to meet, or the pane reads "no location matches" for the very path
// tab just completed.
func TestLauncherResolvesACompletedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scratch"), 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	dir := filepath.Join(root, "scratch")

	m, fb := seedLauncher(t, 100, 20)
	fb.ResolveDirLoc = core.LaunchLocation{Dir: dir, RepoName: "scratch", Branch: "main"}
	m = updLauncher(t, typeInto(t, m, root+"/scr"), coretest.KTab)
	if m.filter != dir+"/" {
		t.Fatalf("tab completed to %q, want %q", m.filter, dir+"/")
	}

	next, cmd := m.Update(core.ReloadTickMsg{Gen: m.resolveGen})
	if cmd == nil {
		t.Fatalf("a completed path should be resolved")
	}
	reply := cmd()
	if len(fb.ResolveDirReq) != 1 || fb.ResolveDirReq[0] != dir {
		t.Fatalf("ResolveLaunchDir asked for %v, want [%s]", fb.ResolveDirReq, dir)
	}
	m = updLauncher(t, next.(Model), reply)
	if got := leftLabels(m); len(got) != 1 || got[0] != "scratch(main)" {
		t.Fatalf("the completed path should list the row it resolved to, got %v", got)
	}
}

// TestLauncherResolvesARelativePath is the same regression a spelling further
// out: ./x is a path the input keeps relative while the row it resolves to holds
// its directory absolutely, so the query has to be read the way the resolve
// canonicalized it or the pane goes empty on the very path tab just completed.
func TestLauncherResolvesARelativePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scratch"), 0o755); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	t.Chdir(root)
	// Getwd rather than root: a temp dir can sit behind a symlink, and it is what
	// filepath.Abs resolves the relative query against.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := filepath.Join(cwd, "scratch")

	m, fb := seedLauncher(t, 100, 20)
	fb.ResolveDirLoc = core.LaunchLocation{Dir: dir, RepoName: "scratch", Branch: "main"}
	m = updLauncher(t, typeInto(t, m, "./scr"), coretest.KTab)
	if m.filter != "./scratch/" {
		t.Fatalf("tab completed to %q, want %q", m.filter, "./scratch/")
	}

	next, cmd := m.Update(core.ReloadTickMsg{Gen: m.resolveGen})
	if cmd == nil {
		t.Fatalf("a completed relative path should be resolved")
	}
	reply := cmd()
	if len(fb.ResolveDirReq) != 1 || fb.ResolveDirReq[0] != dir {
		t.Fatalf("ResolveLaunchDir asked for %v, want [%s]", fb.ResolveDirReq, dir)
	}
	m = updLauncher(t, next.(Model), reply)
	if got := leftLabels(m); len(got) != 1 || got[0] != "scratch(main)" {
		t.Fatalf("a relative path should list the row it resolved to, got %v", got)
	}
	rows, _ := m.leftPane(50, m.matched())
	if len(rows) == 0 || !strings.Contains(rows[0], "scratch") {
		t.Errorf("the resolved row should be on screen, got %q", rows)
	}
}

// TestLauncherLeavesNonPathQueriesAlone pins the other side of the trigger: a
// fuzzy query is not a path, so it never stats or resolves anything.
func TestLauncherLeavesNonPathQueriesAlone(t *testing.T) {
	m, fb := seedLauncher(t, 100, 20)
	m = typeInto(t, m, "webapp")
	if _, cmd := m.Update(core.ReloadTickMsg{Gen: m.resolveGen}); cmd != nil {
		t.Fatalf("a bare word should not be resolved as a directory")
	}
	if len(fb.ResolveDirReq) != 0 {
		t.Errorf("ResolveLaunchDir was asked for %v", fb.ResolveDirReq)
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
		plain := core.StripANSI(row)
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
	fb := &coretest.FakeBackend{LaunchLocs: []core.LaunchLocation{
		{
			Dir: "/home/u/作業場/日本語のディレクトリ名", RepoName: "リポジトリ",
			Branch: "主線", LastUsed: time.Now(), FromHistory: true,
			Projects: []core.LaunchProject{
				// No mux: section, so the file gets " · no mux" appended before it
				// is cut — the right pane's own truncation.
				{Name: "開発環境", File: "構成ファイル/日本語コンポーズ.yaml", FromHistory: true},
				{Name: "テスト", File: "テスト用の長い名前の構成ファイル.yaml", HasMux: true},
			},
		},
		{
			Dir: "/home/u/src/webapp", RepoName: "webapp", Branch: "main",
			LastUsed: time.Now().Add(-time.Hour), FromHistory: true,
			Projects: []core.LaunchProject{{Name: "staging", File: "compose.yaml", HasMux: true}},
		},
	}}

	for w := 24; w <= 100; w++ {
		m := New(context.Background(),
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

// TestShortPathKeepsTheTailInCells covers core.ShortPath on its own: the home
// prefix is abbreviated only on a separator boundary, and whatever it keeps fits
// the cells it was given whether the path is ASCII or double-width. It is
// exercised from here because the launcher's panes are its oldest caller; the
// switcher's heads now write their directories through the same function.
func TestShortPathKeepsTheTailInCells(t *testing.T) {
	for _, p := range []string{
		"/home/u/src/webapp/compose.yaml",
		"/home/u/作業場/日本語のディレクトリ名/構成.yaml",
		"日本語",
		"",
	} {
		for w := range 40 {
			got := core.ShortPath(p, w)
			if core.Cells.StringWidth(got) > w {
				t.Errorf("core.ShortPath(%q, %d) = %q, %d cells wide",
					p, w, got, core.Cells.StringWidth(got))
			}
			if core.Cells.StringWidth(got) != lipgloss.Width(got) {
				t.Errorf("core.ShortPath(%q, %d) = %q measures %d by cells and %d by lipgloss",
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
		if got := core.AbbrevHome(tc.path, home); got != tc.want {
			t.Errorf("core.AbbrevHome(%q, %q) = %q, want %q", tc.path, home, got, tc.want)
		}
	}
	// No home to abbreviate against leaves every path alone.
	if got := core.AbbrevHome("/home/watage/x", ""); got != "/home/watage/x" {
		t.Errorf("core.AbbrevHome with no home = %q, want the path unchanged", got)
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

func TestNewCarriesContext(t *testing.T) {
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "threaded")
	m := New(ctx, core.Options{Backend: &coretest.FakeBackend{}})
	if m.ctx == nil || m.ctx.Value(ctxKey{}) == nil {
		t.Errorf("New should carry the caller's context into the model")
	}
}

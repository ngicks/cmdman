package cmdman_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
)

// Live smoke tests for the widget entrypoint: each `cmdman tui widget <name>`
// must start standalone in a terminal of its own (that is how a frame pane runs
// it), render its own view without the multi-tab chrome, and quit on 'q'.

func TestTUIWidget_SwitcherRendersAndQuits(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	// A never-run project discoverable only through the work directory proves
	// the widget reaches the same project discovery the full TUI uses.
	wd := composeWorkdir(t)
	writeComposeFile(t, wd, composeBasicYAML("widgetsw"))

	w := startWidget(t, ctx, env, wd, "switcher")
	w.waitFor(t, "projects", 5*time.Second)
	// The head is the project's directory, not its name (D44): the path only
	// reaches the column once discovery has found the project sitting there.
	w.waitFor(t, filepath.Base(wd), 5*time.Second)
	// The widget is a single view: the full TUI's tab bar must not be there.
	if snap := w.snapshot(); strings.Contains(snap, "Compose") {
		t.Errorf("switcher widget rendered the full TUI chrome; got:\n%q", snap)
	}
	w.quit(t)
}

// TestTUIWidget_SwitcherSelectionLandsInProjectWindow is the selection gesture
// end to end (D6): enter on a project with no window of its own opens one at the
// project directory and takes the client there, and selecting it again comes
// back to that same window instead of opening a second.
//
// Landing is only observable from inside the multiplexer, so the switcher runs
// the way a frame pane runs it — as a window on a tmux server of the test's own
// (TMUX_TMPDIR keeps the default socket private, and the switcher's own $TMUX
// points the driver at that same server).
func TestTUIWidget_SwitcherSelectionLandsInProjectWindow(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "work", "-n", "home")

	wd := composeWorkdir(t)
	const project = "swland"
	composePath := writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	// create is what puts the project in the listing the switcher reads, and with
	// it the identity a selection addresses. Nothing is brought up, so the project
	// has no window until the selection builds one.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}

	switcher := switcherWindow(t, env, tmuxTmpdir, wd)
	window := "cmdman-" + project
	selectInSwitcher(t, tmuxTmpdir, switcher)
	waitForTmuxWindow(t, tmuxTmpdir, window, 30*time.Second)
	waitForActiveWindow(t, tmuxTmpdir, "work", window, 30*time.Second)

	// The window is the project's own: stamped with the identity the listing
	// carried, which is what makes the next selection a find rather than a build.
	// Which spelling of the work directory the stamp is hashed from is not what
	// this pins — TestMergeProjectInfosStampsIdentity does that.
	want := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()
	if got := tmuxWindowOptionTmpdir(t, tmuxTmpdir, window, "@cmdman_window"); got != want {
		t.Errorf("window identity = %q, want %q", got, want)
	}
	wid := tmuxWindowIDTmpdir(t, tmuxTmpdir, window)
	wantDir, err := filepath.EvalSymlinks(wd)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := tmuxPanesTmpdir(t, tmuxTmpdir, wid, "#{pane_current_path}"); !slices.Equal(
		got, []string{wantDir},
	) {
		t.Errorf("the window built for the project = %v, want one shell at %q", got, wantDir)
	}

	// Walk away and select again: the second selection lands in the window the
	// first one built rather than adding another beside it.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "select-window", "-t", "=work:home")
	selectInSwitcher(t, tmuxTmpdir, switcher)
	waitForActiveWindow(t, tmuxTmpdir, "work", window, 30*time.Second)
	// Both assertions are by id, because by name a reuse and a duplicate look
	// alike: tmux holds any number of windows of one name, and a lookup by name
	// answers with the first of them. So: the client landed on the very window the
	// first selection built, and that window is still the only one of its name.
	if got := activeWindowID(t, tmuxTmpdir, "work"); got != wid {
		t.Errorf("the second selection landed on window %s, want %s", got, wid)
	}
	if got := tmuxWindowIDsTmpdir(t, tmuxTmpdir, window); !slices.Equal(got, []string{wid}) {
		t.Errorf("the second selection built a new window: %q named %q, want only %s",
			got, window, wid)
	}
}

// TestTUIWidget_SwitcherWheelScrollsTheListNotTheCursor is the wheel end to
// end: raw SGR mouse reports reach the widget's stdin, walk the list past what
// the pane can hold, and leave the cursor where it was. Both halves are the
// assertion — a wheel that moved the cursor would scroll the list just the
// same, and the two are only told apart by asking the widget which project it
// is standing on.
//
// It runs as a tmux window because a scrolled list is a screen and not a
// transcript: capture-pane answers with what the pane is showing now, while the
// PTY harness accumulates everything ever drawn and so can never say that a
// line went away.
//
// Nothing puts the terminal in a mouse mode here, and nothing needs to: the
// input decoder parses an SGR mouse report wherever it finds one, so the bytes
// tmux writes into the pane are the same thing a terminal reporting a real
// wheel would write.
func TestTUIWidget_SwitcherWheelScrollsTheListNotTheCursor(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	// The size is pinned rather than left to the default: what this test drives
	// is a list longer than its pane, and a taller window would show the whole
	// thing and leave nothing to scroll.
	tmuxRunWithTmpdir(t, tmuxTmpdir,
		"new-session", "-d", "-s", "work", "-n", "home", "-x", "80", "-y", "24")

	// Two projects, and the tall one is the one the switcher opens on: --workdir
	// below makes it the cwd-active project, which heads the list. Its command
	// rows are what push the second project's head off the bottom of the pane.
	wdTall := composeWorkdir(t)
	const (
		tall  = "swwheela"
		short = "swwheelb"
	)
	tallPath := writeComposeFile(t, wdTall, wheelFillerYAML(tall, switcherWheelRows))
	t.Cleanup(func() { cleanupProject(ctx, env, wdTall, tall) })
	wdShort := composeWorkdir(t)
	shortPath := writeComposeFile(t, wdShort, composeBasicYAML(short))
	t.Cleanup(func() { cleanupProject(ctx, env, wdShort, short) })

	// create and no more: the rows only have to exist to take up lines, and a
	// project nothing was started in leaves no monitor to outlive the test.
	for _, p := range []struct{ wd, path string }{{wdTall, tallPath}, {wdShort, shortPath}} {
		if _, stderr, err := env.muxExecWithTmpdir(
			ctx, tmuxTmpdir, "compose", "--workdir", p.wd, "-f", p.path, "create",
		); err != nil {
			t.Fatalf("compose create in %s failed: %v\nstderr:\n%s", p.wd, err, stderr)
		}
	}

	window := switcherWindow(t, env, tmuxTmpdir, wdTall)

	// The two heads are what the scroll is read by: a head is one line, it names
	// the directory its project sits in (D44), and those directories are this
	// test's own, so on-screen and off-screen are unambiguous for both. The
	// command rows between them are never read — they are there to take up lines.
	tallHead, shortHead := filepath.Base(wdTall), filepath.Base(wdShort)
	onlyTall := func(s string) bool {
		return strings.Contains(s, tallHead) && !strings.Contains(s, shortHead)
	}
	onlyShort := func(s string) bool {
		return !strings.Contains(s, tallHead) && strings.Contains(s, shortHead)
	}
	// The project listing and the command listing arrive as separate messages, so
	// for a moment the list is the two heads and nothing between them — a screen
	// on which the reading below would mean nothing. Waiting for the settled one
	// is what makes "the second head is off the bottom" a fact about the layout.
	waitForPaneShowing(t, tmuxTmpdir, window,
		"the tall project's head with the second project below the fold",
		onlyTall, 20*time.Second)

	// More notches than the list has room to travel, so the offset lands on its
	// bottom clamp: what is on screen afterwards is the end of the list whatever
	// height the pane turned out to have. How far one notch goes is the unit
	// tests' business.
	spinWheel(t, tmuxTmpdir, window, wheelDownSGR, switcherWheelNotches)
	waitForPaneShowing(t, tmuxTmpdir, window,
		"the second project's head with the first scrolled off", onlyShort, 20*time.Second)

	// The cursor is the other half, and it is not on screen to be read — so it is
	// asked. The teardown question names the project under the cursor, and the
	// list showing is the second project's, so the first project's name in that
	// question is the wheel having moved the view and nothing else.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", "=work:"+window, "-l", "D")
	waitForPane(t, tmuxTmpdir, window, "compose down "+tall+"? y/n", 20*time.Second)
	// Anything but y takes the question back, which is how the project survives
	// having been asked about.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", "=work:"+window, "-l", "n")
	waitForPane(t, tmuxTmpdir, window, "compose down "+tall+" cancelled", 20*time.Second)

	// Back up the way it came: the other button code, and the offset clamping at
	// the top rather than running past it.
	spinWheel(t, tmuxTmpdir, window, wheelUpSGR, switcherWheelNotches)
	waitForPaneShowing(t, tmuxTmpdir, window,
		"the tall project's head back at the top", onlyTall, 20*time.Second)
}

// TestTUIWidget_NoQuitSurvivesTheQuitKey is V6's flag end to end — the one a
// frame pane always gets. A docked widget that exits on a keypress leaves a hole
// in the fixture, so q must reach a widget that no longer has it bound, and the
// hint line must stop offering it.
func TestTUIWidget_NoQuitSurvivesTheQuitKey(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	writeComposeFile(t, wd, composeBasicYAML("widgetnq"))

	w := startWidgetEnv(t, ctx, env, wd, wd, "switcher", os.Environ(), "--no-quit")
	w.waitFor(t, filepath.Base(wd), 5*time.Second)
	if snap := w.snapshot(); strings.Contains(snap, "q quit") {
		t.Errorf("a --no-quit switcher must not offer a key it does not have; got:\n%q", snap)
	}
	w.send(t, "q")
	w.mustStayUp(t, time.Second)
}

// TestTUIWidget_LauncherRendersAndQuits is the quick-launch selector's smoke
// test: two panes, the compose project discoverable from the work directory in
// the right one, and q quits.
func TestTUIWidget_LauncherRendersAndQuits(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	writeComposeFile(t, wd, composeBasicYAML("widgetlnch"))

	w := startWidget(t, ctx, env, wd, "launcher")
	w.waitFor(t, "locations", 5*time.Second)
	// The empty input is history mode (D28), and this project has never been
	// brought up — typing is what reaches a location discovered on disk.
	w.send(t, "widgetlnch")
	w.waitFor(t, "widgetlnch", 5*time.Second)
	if snap := w.snapshot(); strings.Contains(snap, "Compose") {
		t.Errorf("launcher widget rendered the full TUI chrome; got:\n%q", snap)
	}
	// A bare letter types into the filter, so the dismissal is ctrl+c.
	w.quitWith(t, "\x03")
}

// TestTUIWidget_LauncherMarksRunningProject is the check a fake cannot make: a
// project brought up with its mux dashboard must read as running in the
// launcher, which only happens when the identity derived from its history row
// is byte-for-byte the one mux stamped on the window. The work directory form
// (canonical, not symlink-resolved) is the whole point.
func TestTUIWidget_LauncherMarksRunningProject(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	// The dashboard is built on tmux's default socket so the launcher, which
	// autodetects the driver, finds the same server; TMUX_TMPDIR keeps that
	// default socket private to this test.
	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	wd := composeWorkdir(t)
	const project = "lnchrun"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	stdout, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "up", "--mux",
	)
	if err != nil {
		t.Fatalf("compose up --mux failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Run the launcher from a directory that is not the project's: a popup is
	// summoned from wherever the user is standing, so nothing about the listing
	// or the marker may depend on the process working directory.
	w := startWidgetEnv(t, ctx, env, wd, t.TempDir(), "launcher", tmuxTmpdirEnv(tmuxTmpdir))
	w.waitFor(t, project, 10*time.Second)
	// The running marker: the hollow circle a project with no reported state
	// carries. A cold entry's slot is blank, so its presence is the assertion.
	w.waitFor(t, "○", 10*time.Second)
	w.quitWith(t, "\x03")
}

// TestTUIWidget_LauncherStartsFromAnywhere drives the launcher's own `s`: a
// project known from history is brought up, dashboard and all, with the
// launcher running in a directory that is not the project's. The window it
// builds must carry the project's identity, which is only true when the action
// path keeps the recorded work directory instead of falling back to its own.
func TestTUIWidget_LauncherStartsFromAnywhere(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	wd := composeWorkdir(t)
	const project = "lnchstart"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// create records the project in history without starting anything, so the
	// launcher lists it and `s` is what brings it up.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}

	w := startWidgetEnv(t, ctx, env, wd, t.TempDir(), "launcher", tmuxTmpdirEnv(tmuxTmpdir))
	w.waitFor(t, project, 10*time.Second)
	w.send(t, "\r") // enter: the input hands the keyboard to the locations list
	w.send(t, "s")  // start the enabled (history) projects here
	window := launcherFallbackWindowName(wd)
	waitForTmuxWindow(t, tmuxTmpdir, window, 30*time.Second)

	// The window name alone would pass even for a dashboard built under the
	// wrong work directory; the ownership stamp is what encodes it. Expected
	// identity is computed from the directory the project was created in, which
	// is not the one the launcher process is standing in.
	want := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()
	if got := tmuxWindowOptionTmpdir(
		t, tmuxTmpdir, window, "@cmdman_window",
	); got != want {
		t.Errorf("dashboard identity = %q, want %q (built under the wrong work dir?)", got, want)
	}
	w.quitWith(t, "\x03")
}

// TestTUIWidget_SwitcherMarksWindowProject is D3's detection end to end: the
// switcher run inside a project's dashboard window must mark that project
// active even though the process is standing somewhere else entirely. No cwd
// match can produce the mark here — the ownership stamp on the enclosing window
// is the only thing that says where the user is.
func TestTUIWidget_SwitcherMarksWindowProject(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	// The dashboard goes on tmux's default socket, kept private by TMUX_TMPDIR,
	// so the widget's driver autodetection reaches the same server.
	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	wd := composeWorkdir(t)
	const project = "swwindow"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "up", "--mux",
	); err != nil {
		t.Fatalf("compose up --mux failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	waitForTmuxWindow(t, tmuxTmpdir, window, 30*time.Second)
	wid := tmuxWindowIDTmpdir(t, tmuxTmpdir, window)

	// The dashboard window is built detached, so it is not its session's current
	// one — and the window probe is client-relative, answering with whatever the
	// session currently displays. Selecting it is what puts the probe in the
	// position a process running in one of its panes would be in.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "select-window", "-t", wid)

	// $TMUX is the whole of what the active-project probe reads: it passes no
	// target and never consults $TMUX_PANE, so the answer follows the session
	// named here (verified against a live tmux). The pane variable addresses a
	// window too — a teardown reads it to tell whether it is inside the window it
	// was asked to close — and tmuxTmpdirEnv strips any inherited one, so nothing
	// here rides on it.
	inWindow := append(tmuxTmpdirEnv(tmuxTmpdir), "TMUX="+tmuxEnvValue(t, tmuxTmpdir, wid))

	// Unrelated on both counts the cwd probe could match: the process directory
	// and the --workdir override.
	elsewhere := t.TempDir()
	w := startWidgetEnv(t, ctx, env, elsewhere, elsewhere, "switcher", inWindow)
	// The head is the project's own directory, so its presence proves the row is
	// the project's — and "active" beside it is the mark under test.
	w.waitFor(t, filepath.Base(wd), 10*time.Second)
	w.waitFor(t, "active", 10*time.Second)
	w.quit(t)
}

// TestTUIWidget_SwitcherSummonsProjectManager is the summon end to end (D7/D9),
// and D17 with it. The switcher runs in a background window of the session whose
// client is displaying project A's dashboard, so every ambient probe inside it
// answers "A"; the cursor is moved to project B, and the panel `m` opens must be
// B's. tmux draws a popup on the attached client, so the client's own terminal
// is where the panel is read — and a client is required at all, since
// display-popup with none does not run (NOTES Q1).
//
// It is also D20: the summon hands the child B's work directory, so the panel
// reads B's replica count and not the zero a load in the popup's own directory
// would find.
func TestTUIWidget_SwitcherSummonsProjectManager(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	// The service names are what tell the two panels apart on screen: B's names
	// nothing the client is already displaying, so reading it there is proof the
	// popup is B's and not the enclosing window's.
	const (
		projectA, serviceA = "pmsuma", "alphasvc"
		projectB, serviceB = "pmsumb", "bravosvc"
	)
	wdA := composeWorkdir(t)
	pathA := writeComposeFile(t, wdA, summonMuxYAML(projectA, serviceA, 1))
	t.Cleanup(func() { cleanupProject(ctx, env, wdA, projectA) })
	// B is the scaled one: its ×3 is a count of B's own stored commands, so it
	// can only be read by a panel whose load stands in B's work directory.
	wdB := composeWorkdir(t)
	pathB := writeComposeFile(t, wdB, summonMuxYAML(projectB, serviceB, 3))
	t.Cleanup(func() { cleanupProject(ctx, env, wdB, projectB) })

	// A gets the dashboard the switcher will sit inside; B is only created, so it
	// is listed with its compose file and has no window of its own anywhere —
	// nothing about B is reachable through the multiplexer.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wdA, "-f", pathA, "up", "--mux",
	); err != nil {
		t.Fatalf("compose up --mux failed: %v\nstderr:\n%s", err, stderr)
	}
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wdB, "-f", pathB, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}
	windowA := "cmdman-" + projectA
	waitForTmuxWindow(t, tmuxTmpdir, windowA, 30*time.Second)

	// What the client displays is what the ambient probe answers with, so it is
	// put on A's dashboard before anything reads it.
	sessionA := tmuxSessionOfWindow(t, tmuxTmpdir, windowA)
	tmuxRunWithTmpdir(t, tmuxTmpdir, "select-window", "-t",
		tmuxWindowIDTmpdir(t, tmuxTmpdir, windowA))
	client := attachTmuxClient(t, ctx, tmuxTmpdir, sessionA)

	// The switcher goes in a window of its own, left in the background so the
	// client keeps looking at A: a switcher that believes it sits in project A,
	// standing in a directory that is neither project's.
	elsewhere := t.TempDir()
	// -a puts it after the window the session is on rather than at that window's
	// own index, which tmux refuses as "index in use".
	pane := tmuxRunWithTmpdir(t, tmuxTmpdir,
		"new-window", "-d", "-a", "-t", "="+sessionA+":", "-P", "-F", "#{pane_id}",
		"-e", cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		"-e", cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		"-e", cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
		cmdmanBin+" tui widget switcher --workdir "+elsewhere,
	)
	waitForPaneText(t, tmuxTmpdir, pane, "active", 20*time.Second)
	waitForPaneText(t, tmuxTmpdir, pane, filepath.Base(wdB), 10*time.Second)

	if snap := client.snapshot(); strings.Contains(snap, serviceB) ||
		strings.Contains(snap, "×3") {
		t.Fatalf("%s or its replica count was on the client screen before the "+
			"summon, so its presence proves nothing; got:\n%q", serviceB, snap)
	}

	// A is the active project, so it heads the list — and it is also the earlier
	// name, so one row down is B either way.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", pane, "j")
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", pane, "m")

	// The service name says the panel is B's; the count says its load stood in
	// B's work directory. A summon that dropped the workdir renders the name off
	// the spec all the same and the row reads ×0.
	deadline := time.Now().Add(20 * time.Second)
	for {
		snap := client.snapshot()
		if strings.Contains(snap, serviceB) && strings.Contains(snap, "×3") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the summoned panel never showed %q at ×3 on the attached "+
				"client.\nclient:\n%q\nswitcher pane:\n%s",
				serviceB, snap, capturePane(t, tmuxTmpdir, pane))
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The popup holds the keyboard while it is up, so the quit key reaches it
	// through the client rather than through the switcher's pane.
	client.send(t, "q")
}

// TestTUIWidget_SwitcherSummonWithNoPopupSaysWhy is the summon's other outcome
// (D4/PLAN step 6): outside a multiplexer there is no popup to open, and the
// switcher says so on its hint line rather than leaving m looking dead. Every
// layer under the key is the real one here — SummonProjectManager, the popup
// seam's driver inference, and tmux itself, whose complaint popupDiag folds
// into the error the widget renders.
func TestTUIWidget_SwitcherSummonWithNoPopupSaysWhy(t *testing.T) {
	// The binary, not a server: the popup fails either way, but only a tmux that
	// ran has something to say about it, and the folding of what it said is what
	// this test pins.
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	const project = "swpopup"
	writeComposeFile(t, wd, composeBasicYAML(project))

	// tmuxTmpdirEnv over a fresh directory is both halves of "outside any
	// multiplexer": $TMUX and $ZELLIJ are stripped, so nothing points the widget
	// at a server, and TMUX_TMPDIR redirects the default socket to a directory
	// that holds none — against the developer's own the popup would really open.
	w := startWidgetEnv(t, ctx, env, wd, wd, "switcher", tmuxTmpdirEnv(t.TempDir()))
	w.waitFor(t, filepath.Base(wd), 10*time.Second)

	// The message is one line and wider than the default 80 columns. What the
	// renderer clips is never written to the terminal at all, so the widening
	// comes before the key rather than after it: a resize afterwards would redraw
	// the view, but only if the widget still holds the message it clipped.
	w.resize(t, 20, 200)
	w.send(t, "m")

	// The switcher's own framing, the seam the summon went through, and then the
	// parentheses — popupDiag puts them there only when the popup process wrote
	// to stderr and that output was folded into the error (cmdman/cli/tui.go:430).
	// Which words tmux picks for a missing server is tmux's business and is not
	// asserted.
	for _, want := range []string{"manage " + project, "popup failed", "(exit status"} {
		w.waitFor(t, want, 20*time.Second)
	}
	// A summon that found no popup is a message, not an ending.
	w.quit(t)
}

// summonMuxYAML is a one-service project whose service name is unique to it, so
// that name on a screen says which project's panel is being read. replicas > 1
// declares a scale:, which is what makes the panel's ×N a reading of the store:
// the service name alone comes off the spec and is the same string whatever
// directory the load ran in.
func summonMuxYAML(project, service string, replicas int) string {
	var scale string
	if replicas > 1 {
		scale = fmt.Sprintf("    scale: %d\n", replicas)
	}
	return fmt.Sprintf(`name: %s
commands:
  %s:
    args: [sleep, "300"]
%smux:
  layouts:
    - name: solo
      root:
        command: %s
`, project, service, scale, service)
}

// tmuxSessionOfWindow names the session the window lives in on the
// default-socket server under tmuxTmpdir.
func tmuxSessionOfWindow(t *testing.T, tmuxTmpdir, window string) string {
	t.Helper()
	listing := tmuxRunWithTmpdir(t, tmuxTmpdir,
		"list-windows", "-a", "-F", "#{window_name}\t#{session_name}")
	for line := range strings.SplitSeq(listing, "\n") {
		if name, session, ok := strings.Cut(line, "\t"); ok && name == window {
			return session
		}
	}
	t.Fatalf("window %q not found; listing:\n%s", window, listing)
	return ""
}

// attachTmuxClient attaches a real client to the session under a pty and
// captures what tmux draws on it.
func attachTmuxClient(
	t *testing.T,
	ctx context.Context,
	tmuxTmpdir, session string,
) *widgetSession {
	t.Helper()
	cmd := exec.CommandContext(ctx, "tmux", "attach-session", "-t", "="+session)
	cmd.Env = append(tmuxTmpdirEnv(tmuxTmpdir), "TERM=xterm-256color")
	c := ptySession(t, cmd, 40, 140)
	waitForSessionAttached(t, tmuxTmpdir, session, 10*time.Second)
	return c
}

// waitForPaneText polls a pane until it renders what — waitForPane's job for a
// pane addressed by id, since the session this one lives in is the project's
// own rather than the landing tests' fixed "work".
func waitForPaneText(t *testing.T, tmuxTmpdir, pane, what string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		cmd := exec.Command("tmux", "capture-pane", "-p", "-t", pane)
		cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
		out, err := cmd.CombinedOutput()
		last = string(out)
		if err == nil && strings.Contains(last, what) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pane %s never rendered %q; last capture:\n%s", pane, what, last)
}

// tmuxEnvValue builds the $TMUX value tmux exports into the panes of windowID's
// session: "<socket path>,<server pid>,<session number>".
func tmuxEnvValue(t *testing.T, tmuxTmpdir, windowID string) string {
	t.Helper()
	out := tmuxRunWithTmpdir(t, tmuxTmpdir, "display-message", "-p", "-t", windowID,
		"#{socket_path}\t#{pid}\t#{session_id}")
	fields := strings.Split(out, "\t")
	if len(fields) != 3 {
		t.Fatalf("unexpected tmux display-message output %q", out)
	}
	// #{session_id} renders as "$0"; $TMUX carries the bare number.
	return fields[0] + "," + fields[1] + "," + strings.TrimPrefix(fields[2], "$")
}

// tmuxWindowOptionTmpdir reads a window option from the default-socket server
// under tmuxTmpdir, addressing the window by name.
func tmuxWindowOptionTmpdir(t *testing.T, tmuxTmpdir, windowName, option string) string {
	t.Helper()
	listing := tmuxRunWithTmpdir(t, tmuxTmpdir,
		"list-windows", "-a", "-F", "#{window_name}\t#{window_id}")
	for line := range strings.SplitSeq(listing, "\n") {
		name, id, ok := strings.Cut(line, "\t")
		if !ok || name != windowName {
			continue
		}
		cmd := exec.Command("tmux", "show-options", "-w", "-t", id, "-v", option)
		cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	t.Fatalf("window %q not found; listing:\n%s", windowName, listing)
	return ""
}

// waitForTmuxWindow polls the default-socket server under tmuxTmpdir until a
// window of that name exists. Errors are transient here — the server does not
// exist until the launcher builds the dashboard.
func waitForTmuxWindow(t *testing.T, tmuxTmpdir, name string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		cmd := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_name}")
		cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
		out, err := cmd.CombinedOutput()
		last = strings.TrimSpace(string(out))
		if err == nil && slices.Contains(strings.Split(last, "\n"), name) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("window %q never appeared; last listing:\n%s", name, last)
}

// tmuxWindowIDsTmpdir returns the @ids of every window of that name across the
// default-socket server under tmuxTmpdir. A name is not unique to tmux, so this
// is what tells "the window is still the one it was" from "another one was built
// beside it", which a lookup by name cannot see.
func tmuxWindowIDsTmpdir(t *testing.T, tmuxTmpdir, window string) []string {
	t.Helper()
	listing := tmuxRunWithTmpdir(t, tmuxTmpdir,
		"list-windows", "-a", "-F", "#{window_name}\t#{window_id}")
	var ids []string
	for line := range strings.SplitSeq(listing, "\n") {
		if name, id, ok := strings.Cut(line, "\t"); ok && name == window {
			ids = append(ids, id)
		}
	}
	return ids
}

// activeWindowID is activeWindowName in ids: which window the session is
// showing, said in the one term that distinguishes same-named windows.
func activeWindowID(t *testing.T, tmuxTmpdir, session string) string {
	t.Helper()
	listing := tmuxRunWithTmpdir(t, tmuxTmpdir,
		"list-windows", "-t", "="+session, "-F", "#{window_id}\t#{window_active}")
	for line := range strings.SplitSeq(listing, "\n") {
		if id, active, ok := strings.Cut(line, "\t"); ok && active == "1" {
			return id
		}
	}
	t.Fatalf("session %s has no active window; listing:\n%s", session, listing)
	return ""
}

// switcherWindow runs the switcher as a tmux window on the test's server — the
// shape a frame pane gives it, and the only one where landing means anything —
// and returns its window name. The -d keeps it off the session's focus, so a
// selection is the only thing that can move focus onto the project's window.
func switcherWindow(t *testing.T, env *testEnv, tmuxTmpdir, workDir string) string {
	t.Helper()
	name := fmt.Sprintf("switcher-%d", time.Now().UnixNano())
	tmuxRunWithTmpdir(t, tmuxTmpdir,
		"new-window", "-d", "-t", "=work", "-n", name,
		"-e", cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		"-e", cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		"-e", cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
		cmdmanBin+" tui widget switcher --workdir "+workDir,
	)
	// --workdir makes the project the cwd-active one, so it heads the list and
	// the switcher opens its cursor on it. Its directory is what the row shows.
	waitForPane(t, tmuxTmpdir, name, filepath.Base(workDir), 20*time.Second)
	return name
}

// selectInSwitcher presses enter on the group under the switcher's cursor.
func selectInSwitcher(t *testing.T, tmuxTmpdir, window string) {
	t.Helper()
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", "=work:"+window, "Enter")
}

// The button codes an SGR mouse report carries for the two wheel directions.
const (
	wheelUpSGR   = 64
	wheelDownSGR = 65
)

// switcherWheelRows is how many command rows the tall project contributes: more
// than the list rows an 80x24 pane has left after the title line and the hint
// line, so the second project's head starts below the fold.
const switcherWheelRows = 24

// switcherWheelNotches is spun in either direction — enough that the offset ends
// on its clamp rather than somewhere the pane's exact height decides.
const switcherWheelNotches = 6

// spinWheel reports n wheel notches over the switcher's list. The bytes are an
// SGR mouse report — ESC [ < button ; column ; row M, both coordinates
// one-based and pane-local — written into the pane literally, which is what a
// terminal reporting a wheel does.
func spinWheel(t *testing.T, tmuxTmpdir, window string, button, n int) {
	t.Helper()
	report := fmt.Sprintf("\x1b[<%d;5;10M", button)
	for range n {
		tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", "=work:"+window, "-l", report)
	}
}

// waitForPaneShowing is waitForPane over a predicate: a scrolled list is as much
// about the line that left the screen as about the one that arrived, and a
// substring search alone cannot say that something is gone. what names the
// screen being waited for, for the failure message.
func waitForPaneShowing(
	t *testing.T,
	tmuxTmpdir, window, what string,
	showing func(string) bool,
	deadline time.Duration,
) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		last = capturePane(t, tmuxTmpdir, "=work:"+window)
		if showing(last) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("window %q never showed %s; last capture:\n%s", window, what, last)
}

// wheelFillerYAML is a project of n commands that are never started. Only the
// count matters: the rows are there to make the list taller than the pane.
func wheelFillerYAML(project string, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\ncommands:\n", project)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "  filler%02d:\n    args: [sleep, \"300\"]\n", i)
	}
	return b.String()
}

// launcherMuxYAML is a project with a mux: section on tmux's default socket, so
// the dashboard lands on the server the launcher will look at.
func launcherMuxYAML(project string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, "300"]
mux:
  layouts:
    - name: solo
      root:
        command: alpha
`, project)
}

// widgetSession is a widget running under a PTY, with its output captured.
type widgetSession struct {
	cmd  *exec.Cmd
	ptmx *os.File

	mu  sync.Mutex
	out bytes.Buffer
}

// startWidget launches `cmdman tui widget <name>` under a PTY, scoped to
// workDir so project discovery is the test's own.
func startWidget(
	t *testing.T,
	ctx context.Context,
	env *testEnv,
	workDir, name string,
) *widgetSession {
	t.Helper()
	return startWidgetEnv(t, ctx, env, workDir, workDir, name, os.Environ())
}

// startWidgetEnv is startWidget over an explicit process directory and base
// environment: for a widget that has to reach the same multiplexer server the
// test drives (TMUX_TMPDIR), and for one whose resolution must be shown not to
// ride on the process directory happening to be the target directory — a popup
// runs wherever it was summoned from.
//
// An empty workDir omits --workdir entirely, which is the documented token
// binding: the key sends nothing but --mux-token, so the process directory is
// the only work directory the invocation carries.
func startWidgetEnv(
	t *testing.T,
	ctx context.Context,
	env *testEnv,
	workDir, runDir, name string,
	baseEnv []string,
	extraArgs ...string,
) *widgetSession {
	t.Helper()

	args := []string{"tui", "widget", name}
	if workDir != "" {
		args = append(args, "--workdir", workDir)
	}
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, cmdmanBin, args...)
	cmd.Dir = runDir
	// The same three the test env pins for every other invocation: without the
	// config path the widget would read the developer's own config.
	cmd.Env = append(slices.Clone(baseEnv),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
		"TERM=xterm-256color")

	return ptySession(t, cmd, 20, 80)
}

// ptySession starts cmd under a pty of the given size and captures everything
// written to it. Both a widget under test and an attached tmux client are read
// this way: a popup is drawn on the client's own terminal, so that terminal is
// the only place its content can be read from.
func ptySession(t *testing.T, cmd *exec.Cmd, rows, cols uint16) *widgetSession {
	t.Helper()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		t.Fatalf("start %s under a pty: %v", cmd.Path, err)
	}
	t.Cleanup(func() { ptmx.Close() })

	w := &widgetSession{cmd: cmd, ptmx: ptmx}
	go func() {
		b := make([]byte, 8192)
		for {
			n, rerr := ptmx.Read(b)
			if n > 0 {
				w.mu.Lock()
				w.out.Write(b[:n])
				w.mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	return w
}

func (w *widgetSession) snapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.String()
}

func (w *widgetSession) waitFor(t *testing.T, what string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if strings.Contains(w.snapshot(), what) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("widget never rendered %q; got:\n%q", what, w.snapshot())
}

// send writes keystrokes to the widget's terminal.
func (w *widgetSession) send(t *testing.T, keys string) {
	t.Helper()
	if _, err := w.ptmx.Write([]byte(keys)); err != nil {
		t.Fatalf("write %q to the widget: %v", keys, err)
	}
}

// resize changes the widget's terminal size. The renderer only writes the cells
// that changed, so a value updated in place never reaches the captured stream as
// the whole row it sits in; a resize is what makes the widget draw the screen it
// is currently showing rather than the difference from the last one.
func (w *widgetSession) resize(t *testing.T, rows, cols uint16) {
	t.Helper()
	if err := pty.Setsize(w.ptmx, &pty.Winsize{Rows: rows, Cols: cols}); err != nil {
		t.Fatalf("resize the widget's terminal to %dx%d: %v", rows, cols, err)
	}
}

func (w *widgetSession) quit(t *testing.T) {
	t.Helper()
	w.quitWith(t, "q")
}

// mustStayUp is quitWith's opposite: it proves the widget outlived the keys sent
// to it. The process is killed afterwards, since the run it survived is over.
func (w *widgetSession) mustStayUp(t *testing.T, d time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- w.cmd.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("widget exited (%v) though its quit keys were unbound; got:\n%q",
			err, w.snapshot())
	case <-time.After(d):
	}
	_ = w.cmd.Process.Kill()
}

// quitWith dismisses the widget with the given keys. Which key that is belongs
// to the widget: the docked ones quit on q, while in the launcher a bare letter
// types into its filter and the dismissal is ctrl+c (or esc).
func (w *widgetSession) quitWith(t *testing.T, keys string) {
	t.Helper()
	_, _ = w.ptmx.Write([]byte(keys))
	done := make(chan error, 1)
	go func() { done <- w.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("widget exited with error: %v\noutput:\n%q", err, w.snapshot())
		}
	case <-time.After(4 * time.Second):
		_ = w.cmd.Process.Kill()
		t.Fatalf("widget did not quit on %q within 4s; got:\n%q", keys, w.snapshot())
	}
}

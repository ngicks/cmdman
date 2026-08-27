package cmdman_test

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// The launcher's two teardown keys, driven against a live multiplexer: `D`y
// takes the project's commands away and `d` takes its dashboard away, and both
// have to leave the row startable again — a row still marked running is a row
// the next `s` skips as already up, which is the one project the user just took
// down.
//
// What each key leaves on the server is the other half. A teardown asked for
// through the TUI removes the window cmdman opened rather than emptying it: the
// gesture said the dashboard should be gone, and a window holding a stray shell
// is not gone. A window cmdman only borrowed — the one the user was sitting in
// when `compose mux up` took it over — is handed back instead, because closing
// it would end the shell they are in.
//
// Only the server can answer either question, so these tests bring the launcher
// up against a real tmux and read the windows afterwards. Tests that keep the
// launcher alive across a teardown drive it under a PTY (startWidgetEnv) rather
// than as a tmux window, since the key that presses `s` a second time has to
// reach a process the landing did not dismiss.

// TestLauncherDown_ComposeDownRemovesTheWindowAndSStartsItAgain is the round
// trip `D`y is for: the project comes up with a dashboard, the teardown takes
// both the commands and the window, and `s` on the same row builds them again.
//
// The second bring-up is asserted as a *different* window rather than as "a
// window": the same id coming back would mean the first one was never removed,
// which is precisely the state the teardown is supposed to leave behind.
func TestLauncherDown_ComposeDownRemovesTheWindowAndSStartsItAgain(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	// A session of the test's own, so the server outlives the project window.
	// tmux exits with its last window, and a server that is gone answers "no
	// such window" for reasons that have nothing to do with the teardown.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "keep", "-n", "home")

	wd := composeWorkdir(t)
	const project = "lnchdrop"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// create records the project in history, so the launcher lists it enabled
	// and every gesture after this is the launcher's own.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}
	identity := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()

	w := startWidgetEnv(t, ctx, env, wd, t.TempDir(), "launcher", tmuxTmpdirEnv(tmuxTmpdir))
	w.waitFor(t, project, 10*time.Second)
	w.send(t, "\r") // enter: the input hands the keyboard to the locations list
	w.send(t, "s")
	// The marker before the window: it is the row learning the bring-up
	// finished, and a teardown that overtook that reply would be undone by it.
	w.waitFor(t, "○", 40*time.Second)
	first := waitForStampedWindow(t, tmuxTmpdir, identity, 30*time.Second)

	// `D` asks before it acts, so the question has to be on screen before the
	// answer is sent — otherwise y is spent on a launcher that never asked.
	w.send(t, "D")
	w.waitFor(t, project+"? y/n", 10*time.Second)
	w.send(t, "y")
	// The counts are the teardown's own line, and waiting for them is what puts
	// the window reading after the teardown rather than beside it.
	w.waitFor(t, "stopped 1, removed 1", 40*time.Second)

	waitForNoStampedWindow(t, tmuxTmpdir, identity, 20*time.Second)
	if ids := windowIDsTmpdir(t, tmuxTmpdir); slices.Contains(ids, first) {
		t.Fatalf("window %s is still on the server after the teardown; windows: %v", first, ids)
	}

	// The same key on the same row: a row left marked running would answer
	// "already up here" and never reach the backend.
	w.send(t, "s")
	second := waitForStampedWindow(t, tmuxTmpdir, identity, 40*time.Second)
	if second == first {
		t.Errorf("the restart landed on window %s, the very one the teardown removed", first)
	}

	w.quitWith(t, "\x03")
}

// TestLauncherDown_MuxDownRemovesTheWindowAndKeepsCommands is `d`: the
// dashboard is only a viewer, so its window goes away whole — not emptied into
// a shell — while the commands it was viewing keep running.
//
// Both halves are asserted by identity: the same command ids running afterwards
// (fresh ids would be a restart, not survival), and no window left carrying the
// project's stamp.
func TestLauncherDown_MuxDownRemovesTheWindowAndKeepsCommands(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "keep", "-n", "home")

	wd := composeWorkdir(t)
	const project = "lnchmuxd"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}
	identity := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()

	w := startWidgetEnv(t, ctx, env, wd, t.TempDir(), "launcher", tmuxTmpdirEnv(tmuxTmpdir))
	w.waitFor(t, project, 10*time.Second)
	w.send(t, "\r")
	w.send(t, "s")
	w.waitFor(t, "○", 40*time.Second)
	dashboard := waitForStampedWindow(t, tmuxTmpdir, identity, 30*time.Second)

	before := composeCommandIDs(ctx, t, env, wd, project)
	if len(before) == 0 {
		t.Fatalf("the bring-up registered no commands, so surviving the teardown proves nothing")
	}

	// `d` asks nothing first: nothing supervised is lost.
	w.send(t, "d")
	w.waitFor(t, "commands still running", 40*time.Second)

	waitForNoStampedWindow(t, tmuxTmpdir, identity, 20*time.Second)
	// Gone rather than handed back: a restore would leave the very same window
	// behind with a shell in it, which is what the stamp check alone cannot see.
	if ids := windowIDsTmpdir(t, tmuxTmpdir); slices.Contains(ids, dashboard) {
		t.Fatalf("the dashboard window %s was restored, not removed; windows: %v", dashboard, ids)
	}

	if after := composeCommandIDs(ctx, t, env, wd, project); !slices.Equal(after, before) {
		t.Fatalf("commands after the teardown = %v, want the same ids %v", after, before)
	}
	for _, id := range before {
		if got := env.inspectJSON(ctx, id)["State"]; got != "running" {
			t.Errorf("command %s is %v after the window it was viewed in was killed, want running",
				id, got)
		}
	}

	// The row is startable again, and `s` builds the dashboard back.
	w.send(t, "s")
	rebuilt := waitForStampedWindow(t, tmuxTmpdir, identity, 40*time.Second)
	if rebuilt == dashboard {
		t.Errorf("the rebuild landed on window %s, the very one `d` removed", dashboard)
	}

	w.quitWith(t, "\x03")
}

// TestLauncherDown_MuxDownRestoresABorrowedWindow is the exception the kill is
// bounded by. `compose mux up` typed into a single-pane shell window takes that
// window over instead of opening one, so the window is the user's and not
// cmdman's — and `d`, which closes the windows cmdman opened, has to hand this
// one back rather than close it: the shell it would take down is the one the
// user is sitting in.
//
// The takeover only happens for an invocation that is inside the multiplexer,
// so the up is typed into a real pane the way the supervised-op tests type it,
// on a tmux server of this test's own.
func TestLauncherDown_MuxDownRestoresABorrowedWindow(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := frameCleanupContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	wd := composeWorkdir(t)
	const project = "lnchlend"
	composePath := writeComposeFile(t, wd, muxOpSoloComposeYAML(project, socket))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// The commands first: the layout's pane is a viewer of one of them, and the
	// up that borrows the window does not bring them up itself.
	if _, stderr, err := env.exec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "up",
	); err != nil {
		t.Fatalf("compose up failed: %v\nstderr:\n%s", err, stderr)
	}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", defaultTimeout)
	}

	selection := compose.ProjectSelection{WorkDir: wd, Project: project}
	identity := selection.ProjectIdentity()

	// A single-pane shell window, which is what the takeover accepts, and the
	// command typed into it — the shape "the window the user was sitting in"
	// actually has.
	shellWid, shellPane := startMuxOpSession(t, env, socket, "work", selection.MuxWindowName())
	c := sendMuxOpCommand(t, socketSender(t, socket), shellPane, fmt.Sprintf(
		"%s compose --workdir %s -f %s mux up", cmdmanBin, wd, composePath,
	))
	waitForMuxOp(t, "the dashboard never took the shell window over",
		func() string { return tmuxWindowOption(t, socket, shellWid, "@cmdman_window") },
		identity, c)
	waitForWindowSettled(t, socket, shellWid, c)

	// The premise: cmdman did not open this window, and says so on the window
	// itself. Without that the teardown below would be free to close it.
	if got := tmuxWindowOption(t, socket, shellWid, "@cmdman_created"); got != "" {
		t.Fatalf("the borrowed window is stamped created (%q); it was opened, not taken over", got)
	}

	// $TMUX is stripped but TMUX_TMPDIR is left alone (muxlessEnv): the driver
	// comes from the compose file's socket, and redirecting the tmpdir would
	// resolve that socket name under a different path.
	w := startWidgetEnv(t, ctx, env, wd, t.TempDir(), "launcher", muxlessEnv())
	w.waitFor(t, project, 10*time.Second)
	w.send(t, "\r")
	w.send(t, "d")
	w.waitFor(t, "commands still running", 40*time.Second)

	if ids := windowIDsOnSocket(t, socket); !slices.Contains(ids, shellWid) {
		t.Fatalf("the borrowed window %s was closed rather than handed back; windows left: %v",
			shellWid, ids)
	}
	// Handed back whole: no project on it, no border row, and the one pane a
	// restore collapses the dashboard's region to.
	if got := tmuxWindowOption(t, socket, shellWid, "@cmdman_window"); got != "" {
		t.Errorf("the restored window still claims project %q", got)
	}
	if got := tmuxWindowOption(t, socket, shellWid, "pane-border-status"); got == "top" {
		t.Errorf("the restored window still shows the dashboard's pane border row")
	}
	if got := tmuxPaneField(t, socket, shellWid, "#{pane_id}"); len(got) != 1 {
		t.Errorf("the restored window holds panes %v, want the single one it started with", got)
	}

	w.quitWith(t, "\x03")
}

// TestLauncherDown_ComposeDownRemovesTheMuxlessLandingWindow is the project
// with nothing to build a dashboard from. `S` still lands it (D9), in the bare
// shell window the landing synthesizes at the project directory, and that
// window carries the project's stamp like any other — so `D`y has to take it
// away too, or the launcher goes on reading the project as running against a
// window with nothing in it.
//
// The launcher is driven as a tmux window here rather than under a PTY: the
// landing needs a client to move, and this is the one landing that leaves the
// launcher up afterwards, since it stays to say why the window is bare.
func TestLauncherDown_ComposeDownRemovesTheMuxlessLandingWindow(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "work", "-n", "home")

	wd := composeWorkdir(t)
	const project = "lnchbare"
	composePath := writeComposeFile(t, wd, launcherMuxlessYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}
	identity := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()

	launcher := launchAndLand(t, env, tmuxTmpdir, wd, project)
	window := launcherFallbackWindowName(wd)
	waitForActiveWindow(t, tmuxTmpdir, "work", window, 30*time.Second)
	// The launcher stays up only to explain the bare window, which is also what
	// says the landing is over and the keys below reach a settled model.
	waitForPane(t, tmuxTmpdir, launcher, "no mux", 20*time.Second)
	landing := waitForStampedWindow(t, tmuxTmpdir, identity, 10*time.Second)

	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", "=work:"+launcher, "-l", "D")
	waitForPane(t, tmuxTmpdir, launcher, project+"? y/n", 20*time.Second)
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", "=work:"+launcher, "-l", "y")
	waitForPane(t, tmuxTmpdir, launcher, "stopped 1, removed 1", 40*time.Second)

	waitForNoStampedWindow(t, tmuxTmpdir, identity, 20*time.Second)
	if ids := windowIDsTmpdir(t, tmuxTmpdir); slices.Contains(ids, landing) {
		t.Fatalf("the landing window %s survived the teardown; windows: %v", landing, ids)
	}
}

// launcherMuxlessYAML is a project with no mux: section — the one the launcher
// lands in a synthesized shell window. Its command is long-running so the
// teardown's counts are a reading of what it stopped rather than a race with a
// command exiting on its own.
func launcherMuxlessYAML(project string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, "300"]
`, project)
}

// composeCommandIDs returns the ids of a project's commands, sorted so two
// readings are comparable. Which ids they are is what tells a command that
// survived a teardown from one that was replaced by a fresh start.
func composeCommandIDs(
	ctx context.Context,
	t *testing.T,
	e *testEnv,
	workdir, project string,
) []string {
	t.Helper()
	var ids []string
	for _, entry := range e.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+workdir,
		"-l", "cmdman.compose.project="+project,
	) {
		id, _ := entry["ID"].(string)
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// windowsStampedTmpdir returns the ids of every window on the default-socket
// server under tmuxTmpdir carrying identity in its @cmdman_window slot.
//
// A server that is not there answers with none rather than failing: tmux exits
// with its last window, so a teardown asked about immediately afterwards can
// find the server already gone — which is the same fact as "no such window" for
// what these tests ask.
func windowsStampedTmpdir(t *testing.T, tmuxTmpdir, identity string) []string {
	t.Helper()
	var ids []string
	for _, line := range listWindowsTmpdir(t, tmuxTmpdir, "#{window_id}\t#{@cmdman_window}") {
		if id, stamp, ok := strings.Cut(line, "\t"); ok && stamp == identity {
			ids = append(ids, id)
		}
	}
	return ids
}

// windowIDsTmpdir returns every window id on the server, which is what says a
// window is gone as opposed to merely unstamped: a teardown that restored the
// window instead of closing it clears the stamp either way.
func windowIDsTmpdir(t *testing.T, tmuxTmpdir string) []string {
	t.Helper()
	return listWindowsTmpdir(t, tmuxTmpdir, "#{window_id}")
}

// windowIDsOnSocket is windowIDsTmpdir for a dedicated -L socket. It answers
// with none rather than failing when the server is gone, which is what closing
// the last window of the last session leaves behind — the very outcome the
// borrowed-window test is asserting did not happen, so it has to be readable
// rather than fatal.
func windowIDsOnSocket(t *testing.T, socket string) []string {
	t.Helper()
	out, err := exec.Command(
		"tmux", "-L", socket, "list-windows", "-a", "-F", "#{window_id}",
	).CombinedOutput()
	if err != nil {
		return nil
	}
	listing := strings.TrimSpace(string(out))
	if listing == "" {
		return nil
	}
	return strings.Split(listing, "\n")
}

// listWindowsTmpdir lists every window on the default-socket server under
// tmuxTmpdir in the given format, answering with nothing when there is no
// server left to ask.
func listWindowsTmpdir(t *testing.T, tmuxTmpdir, format string) []string {
	t.Helper()
	cmd := exec.Command("tmux", "list-windows", "-a", "-F", format)
	cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	listing := strings.TrimSpace(string(out))
	if listing == "" {
		return nil
	}
	return strings.Split(listing, "\n")
}

// waitForStampedWindow polls until exactly one window carries identity and
// returns its id. Exactly one, because a teardown that left the old window
// behind and a bring-up that opened a second one look alike in any answer that
// takes the first match.
func waitForStampedWindow(
	t *testing.T,
	tmuxTmpdir, identity string,
	deadline time.Duration,
) string {
	t.Helper()
	end := time.Now().Add(deadline)
	var ids []string
	for time.Now().Before(end) {
		if ids = windowsStampedTmpdir(t, tmuxTmpdir, identity); len(ids) == 1 {
			return ids[0]
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("windows stamped %q = %v, want exactly one; windows on the server: %v",
		identity, ids, windowIDsTmpdir(t, tmuxTmpdir))
	return ""
}

// waitForNoStampedWindow polls until no window carries identity — the teardown
// seen from the server rather than from the launcher's status line.
func waitForNoStampedWindow(
	t *testing.T,
	tmuxTmpdir, identity string,
	deadline time.Duration,
) {
	t.Helper()
	end := time.Now().Add(deadline)
	var ids []string
	for time.Now().Before(end) {
		if ids = windowsStampedTmpdir(t, tmuxTmpdir, identity); len(ids) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("windows still stamped %q after the teardown: %v", identity, ids)
}

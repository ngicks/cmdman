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
	if snap := w.snapshot(); strings.Contains(snap, "Layout") {
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
	if snap := w.snapshot(); strings.Contains(snap, "Layout") {
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
	waitForTmuxWindow(t, tmuxTmpdir, "cmdman-"+project, 30*time.Second)

	// The window name alone would pass even for a dashboard built under the
	// wrong work directory; the ownership stamp is what encodes it. Expected
	// identity is computed from the directory the project was created in, which
	// is not the one the launcher process is standing in.
	want := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()
	if got := tmuxWindowOptionTmpdir(
		t, tmuxTmpdir, "cmdman-"+project, "@cmdman_window",
	); got != want {
		t.Errorf("dashboard identity = %q, want %q (built under the wrong work dir?)", got, want)
	}
	w.quitWith(t, "\x03")
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
func startWidgetEnv(
	t *testing.T,
	ctx context.Context,
	env *testEnv,
	workDir, runDir, name string,
	baseEnv []string,
	extraArgs ...string,
) *widgetSession {
	t.Helper()

	args := append([]string{"tui", "widget", name, "--workdir", workDir}, extraArgs...)
	cmd := exec.CommandContext(ctx, cmdmanBin, args...)
	cmd.Dir = runDir
	// The same three the test env pins for every other invocation: without the
	// config path the widget would read the developer's own config.
	cmd.Env = append(slices.Clone(baseEnv),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
		"TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 20, Cols: 80})
	if err != nil {
		t.Fatalf("start %s widget pty: %v", name, err)
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

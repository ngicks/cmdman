package cmdman_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// The launcher's landing (PLAN.md phase 1 step 5, D4/D8/D9/D10): `S` brings the
// project up and puts the caller in front of its window.
//
// Landing is only observable from inside the multiplexer, so these tests run the
// launcher the way a key binding does — as a window on a tmux server of their
// own (TMUX_TMPDIR keeps the default socket private, and the launcher's own
// $TMUX points the driver at that same server). The assertion is tmux's own
// notion of focus: which window the session is showing.

// TestLauncherLanding_FocusesProjectWindow covers the cold landing and the
// idempotent re-landing (D4): the second `S` takes you back to the very same
// window, without rebuilding what is already running.
func TestLauncherLanding_FocusesProjectWindow(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "work", "-n", "home")

	wd := composeWorkdir(t)
	const project = "landmux"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// create records the project in history, so the launcher opens with it
	// enabled and `S` is the only gesture the test has to make.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}

	launchAndLand(t, env, tmuxTmpdir, wd, project)
	window := "cmdman-" + project
	waitForTmuxWindow(t, tmuxTmpdir, window, 30*time.Second)
	waitForActiveWindow(t, tmuxTmpdir, "work", window, 30*time.Second)

	wid := tmuxWindowIDTmpdir(t, tmuxTmpdir, window)
	panes := tmuxPanesTmpdir(t, tmuxTmpdir, wid, "#{pane_id}")

	// Walk away and land again. Going back to "home" first is what makes the
	// second landing observable at all, and an idempotent `S` must arrive at the
	// same dashboard rather than rebuild the one that is already up (D4).
	tmuxRunWithTmpdir(t, tmuxTmpdir, "select-window", "-t", "=work:home")
	launchAndLand(t, env, tmuxTmpdir, wd, project)
	waitForActiveWindow(t, tmuxTmpdir, "work", window, 30*time.Second)

	if got := tmuxWindowIDTmpdir(t, tmuxTmpdir, window); got != wid {
		t.Errorf("window id drifted across landings: %s vs %s", wid, got)
	}
	if got := tmuxPanesTmpdir(t, tmuxTmpdir, wid, "#{pane_id}"); !slices.Equal(got, panes) {
		t.Errorf("re-landing rebuilt the panes: %v vs %v", panes, got)
	}
}

// TestLauncherLanding_AttachesFromOutsideTmux is D8's other half: summoned from
// a bare shell there is no client to switch, so the launcher hands its own
// terminal to the multiplexer — the session it built ends up attached, showing
// the project's window.
func TestLauncherLanding_AttachesFromOutsideTmux(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	wd := composeWorkdir(t)
	const project = "landattach"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}

	// TMUX is stripped from the launcher's environment (tmuxTmpdirEnv), which is
	// exactly what "summoned from outside tmux" means to the driver.
	w := startWidgetEnv(t, ctx, env, wd, t.TempDir(), "launcher", tmuxTmpdirEnv(tmuxTmpdir))
	w.waitFor(t, project, 10*time.Second)
	w.send(t, "\r") // the input hands the keyboard to the locations list
	w.send(t, "S")

	// Outside tmux the session is the default "cmdman" one the bring-up creates.
	window := "cmdman-" + project
	waitForTmuxWindow(t, tmuxTmpdir, window, 30*time.Second)
	waitForSessionAttached(t, tmuxTmpdir, "cmdman", 30*time.Second)
	waitForActiveWindow(t, tmuxTmpdir, "cmdman", window, 30*time.Second)

	// Detaching gives the terminal back; the launcher's work is over, so it goes.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "detach-client", "-s", "=cmdman")
	w.quitWith(t, "")
}

// TestLauncherLanding_MuxlessProjectGetsShellWindow is D9: a project whose
// compose file has no mux: section is brought up and landed on anyway, in a
// synthesized one-pane shell window at the project directory, and the launcher
// says why the window is bare instead of leaving the user guessing.
func TestLauncherLanding_MuxlessProjectGetsShellWindow(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "work", "-n", "home")

	wd := composeWorkdir(t)
	const project = "landnomux"
	composePath := writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}

	launcher := launchAndLand(t, env, tmuxTmpdir, wd, project)
	window := "cmdman-" + project
	waitForTmuxWindow(t, tmuxTmpdir, window, 30*time.Second)
	waitForActiveWindow(t, tmuxTmpdir, "work", window, 30*time.Second)

	// The synthesized window is the project's own — stamped, so the launcher
	// reads it as running and `mux down` can restore it — and its shell opens
	// where the project lives.
	if got := tmuxWindowOptionTmpdir(t, tmuxTmpdir, window, "@cmdman_window"); got == "" {
		t.Errorf("the synthesized window carries no ownership stamp")
	}
	wid := tmuxWindowIDTmpdir(t, tmuxTmpdir, window)
	wantDir, err := filepath.EvalSymlinks(wd)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := tmuxPanesTmpdir(t, tmuxTmpdir, wid, "#{pane_current_path}"); !slices.Equal(
		got, []string{wantDir},
	) {
		t.Errorf("synthesized shell window panes = %v, want one shell at %q", got, wantDir)
	}

	// The launcher stays up for exactly one reason: to say why the window is
	// bare (D9). It is the only landing that does not dismiss.
	waitForPane(t, tmuxTmpdir, launcher, "no mux", 10*time.Second)
}

// TestLauncherLanding_StaleEntryForgotten is the failure experience (step 6,
// D10/Q12): a history row whose compose file has gone cannot land at all, so the
// reason stays inline in the launcher, and ctrl+d removes both the row on screen
// and the row in the database.
func TestLauncherLanding_StaleEntryForgotten(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	const project = "landstale"
	composePath := writeComposeFile(t, wd, composeBasicYAML(project))
	if _, stderr, err := env.exec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "create",
	); err != nil {
		t.Fatalf("compose create failed: %v\nstderr:\n%s", err, stderr)
	}
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	// The file moves away; the history row keeps pointing at where it was, which
	// is the stale entry the launcher has to explain.
	moved := filepath.Join(t.TempDir(), "cmd-compose.yaml")
	if err := os.Rename(composePath, moved); err != nil {
		t.Fatalf("move the compose file away: %v", err)
	}
	if row := composeHistoryRow(ctx, t, env, wd, project); row.File != composePath {
		t.Fatalf("history should still point at the moved file, got %q", row.File)
	}

	// Run the launcher from a directory that is not the project's, so nothing
	// re-discovers the project by walking into it.
	w := startWidgetEnv(t, ctx, env, wd, t.TempDir(), "launcher", os.Environ())
	w.waitFor(t, project, 10*time.Second)
	w.send(t, "\r") // the input hands the keyboard to the locations list
	w.send(t, "S")  // a stale entry cannot land: the reason surfaces on its row
	// The row's message names the file that is gone; the removal offer sits at
	// the end of the same line and is clipped in a pane this narrow, so the
	// offer's presence is asserted in the launcher unit tests instead.
	w.waitFor(t, "missing", 10*time.Second)

	w.send(t, "\x04") // ctrl+d: forget it
	w.waitFor(t, "removed from history", 10*time.Second)

	st, err := store.OpenStore(ctx, filepath.Join(env.dataHome, "commands.db"), true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if _, err := st.GetComposeHistory(ctx, wd, project); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("the forgotten history row is still in the database (err = %v)", err)
	}

	w.quitWith(t, "\x03")
}

// launchAndLand runs the launcher as a tmux window on the test's server — the
// shape a key binding gives it, and the only one where landing means anything —
// and presses `S` on the location's enabled project. It returns the launcher's
// window name so a test can read what it says afterwards.
func launchAndLand(
	t *testing.T,
	env *testEnv,
	tmuxTmpdir, workDir, project string,
) string {
	t.Helper()
	// -d: the launcher window is never the session's active one, so the landing
	// is the only thing that can move focus onto the project window — a window
	// closing hands focus to a neighbour, and that neighbour could be the very
	// window under test.
	name := fmt.Sprintf("launcher-%d", time.Now().UnixNano())
	tmuxRunWithTmpdir(t, tmuxTmpdir,
		"new-window", "-d", "-t", "=work", "-n", name,
		"-e", cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		"-e", cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		"-e", cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
		cmdmanBin+" tui widget launcher --workdir "+workDir,
	)
	waitForPane(t, tmuxTmpdir, name, project, 20*time.Second)
	if got := activeWindowName(t, tmuxTmpdir, "work"); got == "cmdman-"+project {
		t.Fatalf("the project window is already focused; the landing would prove nothing")
	}
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", "=work:"+name, "Enter")
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", "=work:"+name, "-l", "S")
	return name
}

// waitForPane polls a window's rendered contents until it shows what, which is
// how a test reads a TUI that tmux (not the test) owns the terminal of.
func waitForPane(t *testing.T, tmuxTmpdir, window, what string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		cmd := exec.Command("tmux", "capture-pane", "-p", "-t", "=work:"+window)
		cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
		out, err := cmd.CombinedOutput()
		last = string(out)
		if err == nil && strings.Contains(last, what) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("window %q never showed %q; last capture:\n%s", window, what, last)
}

// waitForSessionAttached polls until a client is attached to the session, which
// is what the terminal handoff looks like from the server's side.
func waitForSessionAttached(t *testing.T, tmuxTmpdir, session string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		cmd := exec.Command("tmux", "list-sessions",
			"-F", "#{session_name}\t#{session_attached}")
		cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
		out, err := cmd.CombinedOutput()
		last = strings.TrimSpace(string(out))
		if err == nil {
			for line := range strings.SplitSeq(last, "\n") {
				if name, attached, ok := strings.Cut(line, "\t"); ok &&
					name == session && attached != "0" {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no client ever attached to session %s; last listing:\n%s", session, last)
}

// activeWindowName returns the name of the window the session is showing.
func activeWindowName(t *testing.T, tmuxTmpdir, session string) string {
	t.Helper()
	listing := tmuxRunWithTmpdir(t, tmuxTmpdir,
		"list-windows", "-t", "="+session, "-F", "#{window_name}\t#{window_active}")
	for line := range strings.SplitSeq(listing, "\n") {
		if name, active, ok := strings.Cut(line, "\t"); ok && active == "1" {
			return name
		}
	}
	t.Fatalf("session %s has no active window; listing:\n%s", session, listing)
	return ""
}

// waitForActiveWindow polls until the session is showing the named window: the
// landing, in tmux's own terms.
func waitForActiveWindow(t *testing.T, tmuxTmpdir, session, window string, deadline time.Duration) {
	t.Helper()
	end := time.Now().Add(deadline)
	var last string
	for time.Now().Before(end) {
		cmd := exec.Command("tmux", "list-windows", "-t", "="+session,
			"-F", "#{window_name}\t#{window_active}")
		cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
		out, err := cmd.CombinedOutput()
		last = strings.TrimSpace(string(out))
		if err == nil {
			for line := range strings.SplitSeq(last, "\n") {
				if name, active, ok := strings.Cut(line, "\t"); ok &&
					active == "1" && name == window {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("session %s never focused window %q; last listing:\n%s", session, window, last)
}

// tmuxWindowIDTmpdir returns the @id of the named window on the default-socket
// server under tmuxTmpdir.
func tmuxWindowIDTmpdir(t *testing.T, tmuxTmpdir, window string) string {
	t.Helper()
	listing := tmuxRunWithTmpdir(t, tmuxTmpdir,
		"list-windows", "-a", "-F", "#{window_name}\t#{window_id}")
	for line := range strings.SplitSeq(listing, "\n") {
		if name, id, ok := strings.Cut(line, "\t"); ok && name == window {
			return id
		}
	}
	t.Fatalf("window %q not found; listing:\n%s", window, listing)
	return ""
}

// tmuxPanesTmpdir returns one format field per pane of the window, in tmux's
// list order.
func tmuxPanesTmpdir(t *testing.T, tmuxTmpdir, windowID, format string) []string {
	t.Helper()
	out := tmuxRunWithTmpdir(t, tmuxTmpdir, "list-panes", "-t", windowID, "-F", format)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

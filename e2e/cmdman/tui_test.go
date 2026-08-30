package cmdman_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman"
)

// tuiCmd builds `cmdman tui` on the terminal these tests read it in.
func tuiCmd(env *testEnv, args ...string) *Cmd {
	return env.Cmd(append([]string{"tui"}, args...)...).
		WithEnv("TERM=xterm-256color").
		WithSize(30, 100)
}

// quitTUI presses 'q' and requires the TUI to go, which is the other half of
// every render assertion: a screen that never comes back is not a working TUI.
func quitTUI(t *testing.T, sess *Session) {
	t.Helper()
	sess.Send("q")
	res, exited := sess.WaitWithin(t, 4*time.Second)
	if !exited {
		t.Fatalf("TUI did not quit on 'q' within 4s; got:\n%q", sess.Output())
	}
	if res.Err != nil {
		t.Fatalf("tui exited with error: %v\noutput:\n%q", res.Err, sess.Output())
	}
}

// Live smoke test for the bubbletea-v2 TUI: launch `cmdman tui` under a PTY,
// confirm it renders the shell (does not hang on startup), responds to a tab
// switch, and quits cleanly on 'q'.
func TestTUISmoke_RendersAndQuits(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	// Give the TUI some content to list.
	id := env.Run(ctx, "tui-smoke", "/bin/sh", "-c", "sleep 60")
	env.waitForState(ctx, id, "running", defaultTimeout)

	sess := tuiCmd(env).StartPTY(ctx, t)

	// 1) It must render the shell within a few seconds (no startup hang).
	sess.Expect(t, "cmdman tui", 5*time.Second)
	for _, want := range []string{"Commands", "Compose", "Filter"} {
		if !strings.Contains(sess.Output(), want) {
			t.Errorf("TUI render missing %q; got:\n%q", want, sess.Output())
		}
	}
	// The running command must flow from the backend into the render (proves the
	// data path, not just the chrome, works under v2).
	if !strings.Contains(sess.Output(), "tui-smoke") {
		t.Errorf("TUI did not list the running command %q; got:\n%q", "tui-smoke", sess.Output())
	}

	// 2) Drive a tab switch (Commands -> Compose) and confirm the screen actually
	// repaints in response to input. Further output at all is the assertion: the
	// renderer repaints the cells that changed, so the new tab's own strings need
	// never reach the stream contiguously.
	mark := len(sess.Output())
	sess.Send("\t")
	waitUntil(t, 5*time.Second, func() bool { return len(sess.Output()) > mark },
		"tab switch produced no further output (input not handled)")

	// Open the filter, type, and escape back out. The pauses keep the escape from
	// arriving in the same read as the text before it, where the input decoder
	// would take it for the head of an escape sequence rather than a bare esc.
	sess.Send("/")
	time.Sleep(100 * time.Millisecond)
	sess.Send("abc")
	time.Sleep(100 * time.Millisecond)
	sess.Send("\x1b")
	time.Sleep(100 * time.Millisecond)

	// 3) Quit cleanly with 'q'.
	quitTUI(t, sess)
}

// --tab validation happens in RunE (via tui.ParseTab) before the TUI launches,
// so a bad value fails fast without a terminal — assert the non-zero exit and
// the error text rather than driving an interactive session.
func TestTUI_TabFlagRejectsBogus(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "tui", "--tab=bogus")
	if !strings.Contains(stderr, "invalid tab") {
		t.Errorf("expected an invalid-tab error, got stderr:\n%s", stderr)
	}
	// The error must list the valid tokens so users can correct it.
	for _, tab := range []string{"commands", "compose"} {
		if !strings.Contains(stderr, tab) {
			t.Errorf("invalid-tab error missing valid value %q; stderr:\n%s", tab, stderr)
		}
	}
}

// A valid --tab opens the TUI on that tab. The Compose tab's body box is titled
// "Compose projects" and is only rendered while the Compose tab is the active
// body (the bare word "Compose" always shows in the tab bar), so it is a robust
// signal that startup honored --tab=compose rather than the default Commands tab.
func TestTUI_TabFlagStartsOnCompose(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	sess := tuiCmd(env, "--tab=compose").StartPTY(ctx, t)

	sess.Expect(t, "cmdman tui", 5*time.Second)
	// The Compose-tab body box title proves we started on the Compose tab.
	sess.Expect(t, "Compose projects", 5*time.Second)

	quitTUI(t, sess)
}

// --workdir overrides the effective work directory used to discover the
// cwd-active compose project. A never-run project defined only by a
// cmd-compose.yaml surfaces in the Compose tab through that discovery path, so
// its appearance proves `cmdman tui --workdir` is wired end-to-end from the
// flag down into the backend's project discovery.
func TestTUI_WorkdirFlagDiscoversComposeProject(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	writeComposeFile(t, wd, composeBasicYAML("tuiwd"))

	sess := tuiCmd(env, "--workdir", wd, "--tab=compose").InDir(wd).StartPTY(ctx, t)

	sess.Expect(t, "Compose projects", 5*time.Second)
	// The never-run project is discoverable only via the workdir's compose file,
	// so listing it proves --workdir reached the backend discovery path.
	sess.Expect(t, "tuiwd", 5*time.Second)

	quitTUI(t, sess)
}

// The TUI's top bar shows the working directory it is scoped to (the process
// CWD, or the --workdir override). Passing --workdir with a distinctive leaf
// directory proves the cwd is surfaced in the top bar end-to-end. The leaf name
// is asserted rather than the whole path because the top bar left-truncates a
// long path, always keeping the leaf visible.
func TestTUI_TopBarShowsCwd(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := filepath.Join(composeWorkdir(t), "cwdmarker")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}

	sess := tuiCmd(env, "--workdir", wd).InDir(wd).StartPTY(ctx, t)

	sess.Expect(t, "cmdman tui", 5*time.Second)
	// The top bar labels the working directory and keeps its leaf visible.
	sess.Expect(t, "cwd:", 5*time.Second)
	sess.Expect(t, "cwdmarker", 5*time.Second)

	quitTUI(t, sess)
}

// TestTUI_PopupRunsTheFullTUI guards the other user of the popup seam: `cmdman
// tui --popup` still opens the multi-tab TUI in a floating pane, over the same
// launcher that now also carries the switcher's summon.
func TestTUI_PopupRunsTheFullTUI(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	wd := composeWorkdir(t)
	writeComposeFile(t, wd, composeBasicYAML("popupfull"))

	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "popup", "-n", "shell")
	client := attachTmuxClient(t, ctx, env, tmuxTmpdir, "popup")

	pane := tmuxRunWithTmpdir(t, tmuxTmpdir,
		"new-window", "-d", "-a", "-t", "=popup:", "-P", "-F", "#{pane_id}",
		"-e", cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		"-e", cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		"-e", cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
		cmdmanBin+" tui --popup --workdir "+wd,
	)

	// The tab bar is the full TUI and nothing else: a widget popup has no tabs.
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(client.Output(), "Compose") {
		if time.Now().After(deadline) {
			t.Fatalf("the popup never rendered the full TUI.\nclient:\n%q\nlauncher pane:\n%s",
				client.Output(), capturePane(t, tmuxTmpdir, pane))
		}
		time.Sleep(100 * time.Millisecond)
	}

	client.Send("q")
}

// The popup geometry flags only apply with --popup; using one without it is
// rejected in RunE before any tmux invocation, so this needs no tmux/terminal.
func TestTUI_PopupGeometryRequiresPopup(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "tui", "--popup-width=80%")
	if !strings.Contains(stderr, "--popup-width") || !strings.Contains(stderr, "--popup") {
		t.Errorf("expected a 'requires --popup' error, got stderr:\n%s", stderr)
	}
}

// A bare numeric geometry value is rejected for not being an explicit
// percentage. Validation (PopupGeometry.Validate) runs before tmux is invoked,
// so the failure does not depend on tmux being installed.
func TestTUI_PopupGeometryRejectsBareNumber(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "tui", "--popup", "--popup-width=80")
	if !strings.Contains(stderr, "--popup-width") || !strings.Contains(stderr, "percentage") {
		t.Errorf("expected a percentage-format error, got stderr:\n%s", stderr)
	}
}

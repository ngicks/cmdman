package cmdman_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/ngicks/cmdman/pkg/cmdman"
)

// Live smoke test for the bubbletea-v2 TUI: launch `cmdman tui` under a PTY,
// confirm it renders the shell (does not hang on startup), responds to a tab
// switch, and quits cleanly on 'q'.
func TestTUISmoke_RendersAndQuits(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	// Give the TUI some content to list.
	id := env.run(ctx, "run", "-n", "tui-smoke", "--", "/bin/sh", "-c", "sleep 60")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	tuiCmd := exec.CommandContext(ctx, cmdmanBin, "tui")
	tuiCmd.Env = append(os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		"TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(tuiCmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatalf("start tui pty: %v", err)
	}
	defer ptmx.Close()

	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 8192)
		for {
			n, rerr := ptmx.Read(b)
			if n > 0 {
				mu.Lock()
				out.Write(b[:n])
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}

	// 1) It must render the shell within a few seconds (no startup hang).
	waitFor := func(what string, deadline time.Duration) {
		t.Helper()
		end := time.Now().Add(deadline)
		for time.Now().Before(end) {
			if strings.Contains(snapshot(), what) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("TUI never rendered %q; got:\n%q", what, snapshot())
	}
	waitFor("cmdman tui", 5*time.Second)
	for _, want := range []string{"Commands", "Compose", "Layout", "Filter"} {
		if !strings.Contains(snapshot(), want) {
			t.Errorf("TUI render missing %q; got:\n%q", want, snapshot())
		}
	}
	// The running command must flow from the backend into the render (proves the
	// data path, not just the chrome, works under v2).
	if !strings.Contains(snapshot(), "tui-smoke") {
		t.Errorf("TUI did not list the running command %q; got:\n%q", "tui-smoke", snapshot())
	}
	t.Logf("initial render ok (%d bytes captured)", len(snapshot()))

	// 2) Drive a tab switch (Commands -> Compose) and confirm the screen
	// actually repaints in response to input.
	before := len(snapshot())
	_, _ = ptmx.Write([]byte("\t"))
	time.Sleep(300 * time.Millisecond)
	if len(snapshot()) <= before {
		t.Errorf("tab switch produced no further output (input not handled)")
	}
	// Open the filter, type, and escape back out.
	_, _ = ptmx.Write([]byte("/"))
	time.Sleep(100 * time.Millisecond)
	_, _ = ptmx.Write([]byte("abc"))
	time.Sleep(100 * time.Millisecond)
	_, _ = ptmx.Write([]byte("\x1b")) // esc out of filter
	time.Sleep(100 * time.Millisecond)

	// 3) Quit cleanly with 'q'.
	_, _ = ptmx.Write([]byte("q"))

	done := make(chan error, 1)
	go func() { done <- tuiCmd.Wait() }()
	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("tui exited with error: %v\noutput:\n%q", werr, snapshot())
		}
	case <-time.After(4 * time.Second):
		_ = tuiCmd.Process.Kill()
		t.Fatalf("TUI did not quit on 'q' within 4s; got:\n%q", snapshot())
	}
	t.Logf("TUI quit cleanly on 'q'")
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
	for _, tab := range []string{"commands", "compose", "layout"} {
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

	tuiCmd := exec.CommandContext(ctx, cmdmanBin, "tui", "--tab=compose")
	tuiCmd.Env = append(os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		"TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(tuiCmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatalf("start tui pty: %v", err)
	}
	defer ptmx.Close()

	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 8192)
		for {
			n, rerr := ptmx.Read(b)
			if n > 0 {
				mu.Lock()
				out.Write(b[:n])
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}

	waitFor := func(what string, deadline time.Duration) {
		t.Helper()
		end := time.Now().Add(deadline)
		for time.Now().Before(end) {
			if strings.Contains(snapshot(), what) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("TUI never rendered %q; got:\n%q", what, snapshot())
	}
	waitFor("cmdman tui", 5*time.Second)
	// The Compose-tab body box title proves we started on the Compose tab.
	waitFor("Compose projects", 5*time.Second)

	// Quit cleanly with 'q'.
	_, _ = ptmx.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- tuiCmd.Wait() }()
	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("tui exited with error: %v\noutput:\n%q", werr, snapshot())
		}
	case <-time.After(4 * time.Second):
		_ = tuiCmd.Process.Kill()
		t.Fatalf("TUI did not quit on 'q' within 4s; got:\n%q", snapshot())
	}
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

	tuiCmd := exec.CommandContext(ctx, cmdmanBin, "tui", "--workdir", wd, "--tab=compose")
	tuiCmd.Dir = wd
	tuiCmd.Env = append(os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		"TERM=xterm-256color")

	ptmx, err := pty.StartWithSize(tuiCmd, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		t.Fatalf("start tui pty: %v", err)
	}
	defer ptmx.Close()

	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		b := make([]byte, 8192)
		for {
			n, rerr := ptmx.Read(b)
			if n > 0 {
				mu.Lock()
				out.Write(b[:n])
				mu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()
	snapshot := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}

	waitFor := func(what string, deadline time.Duration) {
		t.Helper()
		end := time.Now().Add(deadline)
		for time.Now().Before(end) {
			if strings.Contains(snapshot(), what) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("TUI never rendered %q; got:\n%q", what, snapshot())
	}
	waitFor("Compose projects", 5*time.Second)
	// The never-run project is discoverable only via the workdir's compose file,
	// so listing it proves --workdir reached the backend discovery path.
	waitFor("tuiwd", 5*time.Second)

	// Quit cleanly with 'q'.
	_, _ = ptmx.Write([]byte("q"))
	done := make(chan error, 1)
	go func() { done <- tuiCmd.Wait() }()
	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("tui exited with error: %v\noutput:\n%q", werr, snapshot())
		}
	case <-time.After(4 * time.Second):
		_ = tuiCmd.Process.Kill()
		t.Fatalf("TUI did not quit on 'q' within 4s; got:\n%q", snapshot())
	}
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

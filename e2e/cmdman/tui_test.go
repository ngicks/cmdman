package cmdman_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty/v2"
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
	for _, want := range []string{"Commands", "Compose", "Filter"} {
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

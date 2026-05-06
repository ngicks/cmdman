package tmux

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/mux"
)

// tmuxPath resolves the tmux binary. Fatal (not skip) if not found.
func tmuxPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("tmux not found in PATH: %v", err)
	}
	return p
}

// testConfig returns a Config with a unique socket name derived from the test name.
func testConfig(t *testing.T, name string) Config {
	t.Helper()
	socket := strings.ReplaceAll(t.Name(), "/", "_")
	return Config{
		Name:       name,
		TmuxPath:   tmuxPath(t),
		SocketName: socket,
	}
}

// newTestSession creates a new tmux session and registers cleanup to kill the server.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	cfg := testConfig(t, "test")
	ctx := context.Background()

	sess, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(func() {
		e := newExecutor(cfg.TmuxPath, cfg.SocketName)
		// kill-server shuts down the entire server on this socket.
		_, _ = e.run(context.Background(), "kill-server")
	})

	return sess
}

// pollCapture polls Capture until output contains match or timeout expires.
func pollCapture(t *testing.T, p mux.Pane, match string, timeout time.Duration) string {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := p.Capture(ctx, 0, 50)
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}
		if strings.Contains(out, match) {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pollCapture: %q not found within %v", match, timeout)
	return ""
}

func TestTmuxNew(t *testing.T) {
	tmuxPath(t)
	cfg := testConfig(t, "newsess")
	ctx := context.Background()

	sess, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		e := newExecutor(cfg.TmuxPath, cfg.SocketName)
		_, _ = e.run(context.Background(), "kill-server")
	})

	if !strings.HasPrefix(sess.Id(), "$") {
		t.Errorf("session ID = %q, want prefix $", sess.Id())
	}

	// Duplicate session with DisallowReuse should return ErrSessionExists.
	cfgDisallow := cfg
	cfgDisallow.DisallowReuse = true
	_, err = New(ctx, cfgDisallow)
	if !errors.Is(err, mux.ErrSessionExists) {
		t.Errorf("duplicate New (DisallowReuse): got %v, want %v", err, mux.ErrSessionExists)
	}
}

func TestTmuxNewReuse(t *testing.T) {
	tmuxPath(t)
	cfg := testConfig(t, "reusesess")
	ctx := context.Background()

	sess, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		e := newExecutor(cfg.TmuxPath, cfg.SocketName)
		_, _ = e.run(context.Background(), "kill-server")
	})

	// Duplicate session with DisallowReuse=false (default) should succeed via attach.
	sess2, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("duplicate New (reuse): %v", err)
	}
	if sess2.Id() != sess.Id() {
		t.Errorf("reused session ID = %q, want %q", sess2.Id(), sess.Id())
	}
}

func TestTmuxAttach(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()
	cfg := testConfig(t, "test")

	attached, err := Attach(ctx, cfg)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if attached.Id() != sess.Id() {
		t.Errorf("attached ID = %q, want %q", attached.Id(), sess.Id())
	}

	// Nonexistent session should return ErrSessionNotFound.
	badCfg := cfg
	badCfg.Name = "nonexistent"
	_, err = Attach(ctx, badCfg)
	if !errors.Is(err, mux.ErrSessionNotFound) {
		t.Errorf("Attach nonexistent: got %v, want %v", err, mux.ErrSessionNotFound)
	}
}

func TestTmuxSessionName(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	name, err := sess.Name(ctx)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if name != "test" {
		t.Errorf("Name = %q, want %q", name, "test")
	}
}

func TestTmuxSessionNewWindow(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "mywin", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if !strings.HasPrefix(w.Id(), "@") {
		t.Errorf("window ID = %q, want prefix @", w.Id())
	}
	name, err := w.Name(ctx)
	if err != nil {
		t.Fatalf("window.Name: %v", err)
	}
	if name != "mywin" {
		t.Errorf("window name = %q, want %q", name, "mywin")
	}
}

func TestTmuxSessionList(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	// Initially 1 window.
	windows, err := sess.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("initial window count = %d, want 1", len(windows))
	}

	// Add 2 more windows.
	for _, name := range []string{"win1", "win2"} {
		if _, err := sess.NewWindow(ctx, name, nil); err != nil {
			t.Fatalf("NewWindow(%q): %v", name, err)
		}
	}

	windows, err = sess.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(windows) != 3 {
		t.Errorf("window count = %d, want 3", len(windows))
	}
}

func TestTmuxSessionGetAt(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}
	if w.Id() == "" {
		t.Error("GetAt(0) returned empty ID")
	}

	// Out of bounds.
	_, err = sess.GetAt(ctx, 99)
	if !errors.Is(err, mux.ErrWindowNotFound) {
		t.Errorf("GetAt(99): got %v, want %v", err, mux.ErrWindowNotFound)
	}

	// Negative.
	_, err = sess.GetAt(ctx, -1)
	if !errors.Is(err, mux.ErrWindowNotFound) {
		t.Errorf("GetAt(-1): got %v, want %v", err, mux.ErrWindowNotFound)
	}
}

func TestTmuxSessionGetById(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	windows, err := sess.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	knownID := windows[0].Id()

	w, err := sess.GetById(ctx, knownID)
	if err != nil {
		t.Fatalf("GetById(%q): %v", knownID, err)
	}
	if w.Id() != knownID {
		t.Errorf("GetById returned ID = %q, want %q", w.Id(), knownID)
	}

	// Unknown ID.
	_, err = sess.GetById(ctx, "@99999")
	if !errors.Is(err, mux.ErrWindowNotFound) {
		t.Errorf("GetById unknown: got %v, want %v", err, mux.ErrWindowNotFound)
	}
}

func TestTmuxSessionClose(t *testing.T) {
	tmuxPath(t)
	cfg := testConfig(t, "closesess")
	ctx := context.Background()

	sess, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		e := newExecutor(cfg.TmuxPath, cfg.SocketName)
		_, _ = e.run(context.Background(), "kill-server")
	})

	if err := sess.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Attach should fail.
	_, err = Attach(ctx, cfg)
	if !errors.Is(err, mux.ErrSessionNotFound) {
		t.Errorf("Attach after Close: got %v, want %v", err, mux.ErrSessionNotFound)
	}
}

func TestTmuxWindowName(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "namedwin", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	name, err := w.Name(ctx)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if name != "namedwin" {
		t.Errorf("Name = %q, want %q", name, "namedwin")
	}
}

func TestTmuxWindowSplit(t *testing.T) {
	tests := []struct {
		name      string
		splitN    int
		wantPanes int
	}{
		{"split 0 is noop", 0, 1},
		{"split 1 adds 1 pane", 1, 2},
		{"split 3 adds 3 panes", 3, 4},
		// {"split 7 adds 7 panes", 7, 8}, Fails with no space for new pane; detached session does
		// not have enough space.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := newTestSession(t)
			ctx := context.Background()

			w, err := sess.GetAt(ctx, 0)
			if err != nil {
				t.Fatalf("GetAt(0): %v", err)
			}

			if err := w.Split(ctx, tt.splitN); err != nil {
				t.Fatalf("Split(%d): %v", tt.splitN, err)
			}

			panes, err := w.List(ctx)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(panes) != tt.wantPanes {
				t.Errorf("pane count = %d, want %d", len(panes), tt.wantPanes)
			}
		})
	}
}

func TestTmuxWindowList(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}

	// Initially 1 pane.
	panes, err := w.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("initial pane count = %d, want 1", len(panes))
	}

	// Split to add 2 more.
	if err := w.Split(ctx, 2); err != nil {
		t.Fatalf("Split(2): %v", err)
	}

	panes, err = w.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(panes) != 3 {
		t.Errorf("pane count = %d, want 3", len(panes))
	}
}

func TestTmuxWindowGetAtAndGetById(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}

	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}
	if p.Id() == "" {
		t.Error("pane GetAt(0) returned empty ID")
	}

	// GetById with known ID.
	p2, err := w.GetById(ctx, p.Id())
	if err != nil {
		t.Fatalf("pane GetById(%q): %v", p.Id(), err)
	}
	if p2.Id() != p.Id() {
		t.Errorf("GetById returned ID = %q, want %q", p2.Id(), p.Id())
	}

	// Out of bounds.
	_, err = w.GetAt(ctx, 99)
	if !errors.Is(err, mux.ErrPaneNotFound) {
		t.Errorf("pane GetAt(99): got %v, want %v", err, mux.ErrPaneNotFound)
	}

	// Unknown ID.
	_, err = w.GetById(ctx, "%99999")
	if !errors.Is(err, mux.ErrPaneNotFound) {
		t.Errorf("pane GetById unknown: got %v, want %v", err, mux.ErrPaneNotFound)
	}
}

func TestTmuxWindowIndex(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	// Create extra windows so we have multiple.
	if _, err := sess.NewWindow(ctx, "w1", nil); err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	if _, err := sess.NewWindow(ctx, "w2", nil); err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	windows, err := sess.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	for i, w := range windows {
		idx, err := w.Index(ctx)
		if err != nil {
			t.Fatalf("windows[%d].Index: %v", i, err)
		}
		// Index should match position in list (tmux default base-index is 0).
		if idx != i {
			t.Errorf("windows[%d].Index = %d, want %d", i, idx, i)
		}
	}
}

func TestTmuxWindowClose(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	// Add a window so we can close it without killing the session.
	w, err := sess.NewWindow(ctx, "toclose", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	wID := w.Id()

	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Should not be in listing anymore.
	_, err = sess.GetById(ctx, wID)
	if !errors.Is(err, mux.ErrWindowNotFound) {
		t.Errorf("GetById after Close: got %v, want %v", err, mux.ErrWindowNotFound)
	}
}

func TestTmuxPaneName(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	name, err := p.Name(ctx)
	if err != nil {
		t.Fatalf("pane Name: %v", err)
	}
	if name == "" {
		t.Error("pane Name returned empty string")
	}
}

func TestTmuxPaneSendKeysAndCapture(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	if err := p.SendKeys(ctx, []string{"echo hello", "Enter"}); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	pollCapture(t, p, "hello", 3*time.Second)
}

func TestTmuxPaneSendKeysEmpty(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	_ = sess // keep reference

	if err := p.SendKeys(ctx, nil); err != nil {
		t.Errorf("SendKeys(nil): %v", err)
	}
	if err := p.SendKeys(ctx, []string{}); err != nil {
		t.Errorf("SendKeys([]string{}): %v", err)
	}
}

func TestTmuxPaneCaptureEdge(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	_ = sess

	// Capture(0, 0) should not error.
	_, err = p.Capture(ctx, 0, 0)
	if err != nil {
		t.Errorf("Capture(0, 0): %v", err)
	}
}

func TestTmuxPaneIndex(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}

	if err := w.Split(ctx, 2); err != nil {
		t.Fatalf("Split(2): %v", err)
	}

	panes, err := w.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	_ = sess

	for i, p := range panes {
		idx, err := p.Index(ctx)
		if err != nil {
			t.Fatalf("panes[%d].Index: %v", i, err)
		}
		if idx != i {
			t.Errorf("panes[%d].Index = %d, want %d", i, idx, i)
		}
	}
}

func TestTmuxNewInstallsHooks(t *testing.T) {
	tmuxPath(t)
	cfg := testConfig(t, "hooksess")
	ctx := context.Background()

	_, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		e := newExecutor(cfg.TmuxPath, cfg.SocketName)
		_, _ = e.run(context.Background(), "kill-server")
	})

	exec := newExecutor(cfg.TmuxPath, cfg.SocketName)
	out, err := exec.run(ctx, "show-hooks", "-t", cfg.Name)
	if err != nil {
		t.Fatalf("show-hooks: %v", err)
	}

	for _, hook := range []string{"client-attached[100]", "client-detached[100]"} {
		if !strings.Contains(out, hook) {
			t.Errorf("hook %q not found in show-hooks output:\n%s", hook, out)
		}
	}

	// Verify the hook command references the session name and select-layout.
	if !strings.Contains(out, "select-layout") {
		t.Errorf("hook command missing select-layout in show-hooks output:\n%s", out)
	}
	if !strings.Contains(out, "list-windows") {
		t.Errorf("hook command missing list-windows in show-hooks output:\n%s", out)
	}
}

func TestTmuxPaneClose(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}

	// Split to have 2 panes so we can close one.
	if err := w.Split(ctx, 1); err != nil {
		t.Fatalf("Split(1): %v", err)
	}

	panes, err := w.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("pane count = %d, want 2", len(panes))
	}

	closedID := panes[1].Id()
	if err := panes[1].Close(ctx); err != nil {
		t.Fatalf("pane Close: %v", err)
	}

	_ = sess

	// Should not be found.
	_, err = w.GetById(ctx, closedID)
	if !errors.Is(err, mux.ErrPaneNotFound) {
		t.Errorf("GetById after Close: got %v, want %v", err, mux.ErrPaneNotFound)
	}
}

// newTestSessionWithKeys creates a tmux session with startup keys and registers cleanup.
func newTestSessionWithKeys(t *testing.T, startupKeys []string) *Session {
	t.Helper()
	cfg := testConfig(t, "test")
	cfg.StartupKeys = startupKeys
	ctx := context.Background()

	sess, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(func() {
		e := newExecutor(cfg.TmuxPath, cfg.SocketName)
		_, _ = e.run(context.Background(), "kill-server")
	})

	return sess
}

func TestStartupKeysSession(t *testing.T) {
	sess := newTestSessionWithKeys(t, []string{"echo SESSION_MARKER", "Enter"})
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	pollCapture(t, p, "SESSION_MARKER", 5*time.Second)
}

func TestStartupKeysWindow(t *testing.T) {
	sess := newTestSessionWithKeys(t, []string{"echo SESS_KEY", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "keyed", []string{"echo WIN_KEY", "Enter"})
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	// Both session and window keys should appear.
	pollCapture(t, p, "SESS_KEY", 5*time.Second)
	pollCapture(t, p, "WIN_KEY", 5*time.Second)
}

func TestStartupKeysSplit(t *testing.T) {
	sess := newTestSessionWithKeys(t, []string{"echo SPLIT_SESS", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "splitwin", []string{"echo SPLIT_WIN", "Enter"})
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	if err := w.Split(ctx, 1); err != nil {
		t.Fatalf("Split(1): %v", err)
	}

	panes, err := w.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("pane count = %d, want 2", len(panes))
	}

	// The new pane (index 1) should have both keys.
	pollCapture(t, panes[1], "SPLIT_SESS", 5*time.Second)
	pollCapture(t, panes[1], "SPLIT_WIN", 5*time.Second)
}

func TestStartupKeysPersistence(t *testing.T) {
	sess := newTestSessionWithKeys(t, []string{"echo PERSIST_SESS", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "persistwin", []string{"echo PERSIST_WIN", "Enter"})
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	// Re-acquire the window via GetById to simulate reconstruction.
	w2, err := sess.GetById(ctx, w.Id())
	if err != nil {
		t.Fatalf("GetById: %v", err)
	}

	if err := w2.Split(ctx, 1); err != nil {
		t.Fatalf("Split(1): %v", err)
	}

	panes, err := w2.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("pane count = %d, want 2", len(panes))
	}

	// New pane from reconstructed window should still get keys.
	pollCapture(t, panes[1], "PERSIST_SESS", 5*time.Second)
	pollCapture(t, panes[1], "PERSIST_WIN", 5*time.Second)
}

func TestStartupKeysNil(t *testing.T) {
	// Nil/empty startup keys should produce no errors.
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "nilkeys", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	if err := w.Split(ctx, 1); err != nil {
		t.Fatalf("Split(1): %v", err)
	}

	panes, err := w.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(panes) != 2 {
		t.Errorf("pane count = %d, want 2", len(panes))
	}

	_ = sess
}

// newTestSessionWithCleanShell creates a tmux session with startup keys and
// configures bash --norc --noprofile as default-command for reliable testing.
func newTestSessionWithCleanShell(t *testing.T, startupKeys []string) *Session {
	t.Helper()
	cfg := testConfig(t, "test")
	cfg.StartupKeys = startupKeys
	ctx := context.Background()

	sess, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	exec := newExecutor(cfg.TmuxPath, cfg.SocketName)
	_, _ = exec.run(ctx, "set-option", "-g", "default-command", "bash --norc --noprofile")

	t.Cleanup(func() {
		e := newExecutor(cfg.TmuxPath, cfg.SocketName)
		_, _ = e.run(context.Background(), "kill-server")
	})

	return sess
}

func TestInterpolateInjectMeta(t *testing.T) {
	// Use #{INJECT_META} + Enter in session startup keys to export CRAB_* env vars.
	sess := newTestSessionWithCleanShell(t, []string{"#{INJECT_META}", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "metacheck", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	if err := p.SendKeys(ctx, []string{"echo $CRAB_PANE_ID", "Enter"}); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	pollCapture(t, p, "%", 15*time.Second)
}

func TestInterpolateInjectMetaSplit(t *testing.T) {
	sess := newTestSessionWithCleanShell(t, []string{"#{INJECT_META}", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "splitcheck", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	origPane, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	if err := w.Split(ctx, 1); err != nil {
		t.Fatalf("Split(1): %v", err)
	}

	panes, err := w.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("pane count = %d, want 2", len(panes))
	}

	newPane := panes[1]
	if err := newPane.SendKeys(ctx, []string{"echo $CRAB_PANE_ID", "Enter"}); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	out := pollCapture(t, newPane, "%", 15*time.Second)

	if strings.Contains(out, origPane.Id()) {
		t.Errorf("new pane has same CRAB_PANE_ID as original (%s)", origPane.Id())
	}
}

func TestInterpolateSessionID(t *testing.T) {
	sess := newTestSessionWithCleanShell(t, []string{"echo SID=#{SESSION_ID}", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "sidcheck", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	pollCapture(t, p, "SID="+sess.Id(), 15*time.Second)
}

func TestInterpolatePaneID(t *testing.T) {
	sess := newTestSessionWithCleanShell(t, []string{"echo PID=#{PANE_ID}", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "pidcheck", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	// The pane ID should appear in the capture (starts with %).
	pollCapture(t, p, "PID=%", 15*time.Second)

	// Split and verify the new pane gets its own ID.
	if err := w.Split(ctx, 1); err != nil {
		t.Fatalf("Split(1): %v", err)
	}
	panes, err := w.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("pane count = %d, want 2", len(panes))
	}
	pollCapture(t, panes[1], "PID=%", 15*time.Second)
}

func TestInterpolateEscape(t *testing.T) {
	// ##{PANE_ID} should produce the literal text #{PANE_ID}, not the actual pane ID.
	sess := newTestSessionWithCleanShell(t, []string{"echo ESCAPED=##{PANE_ID}", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "esccheck", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	pollCapture(t, p, "ESCAPED=#{PANE_ID}", 15*time.Second)
}

func TestInterpolateSendKeys(t *testing.T) {
	// Verify interpolation works in plain SendKeys (not just startup keys).
	sess := newTestSessionWithCleanShell(t, nil)
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "skcheck", nil)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	if err := p.SendKeys(ctx, []string{"echo SKPID=#{PANE_ID}", "Enter"}); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	pollCapture(t, p, "SKPID=%", 15*time.Second)
}

func TestStartupKeysOrdering(t *testing.T) {
	sess := newTestSessionWithKeys(t, []string{"echo ORDER_A", "Enter"})
	ctx := context.Background()

	w, err := sess.NewWindow(ctx, "orderwin", []string{"echo ORDER_B", "Enter"})
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	p, err := w.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("pane GetAt(0): %v", err)
	}

	// Wait for both to appear.
	out := pollCapture(t, p, "ORDER_B", 5*time.Second)

	// Session keys (ORDER_A) should appear before window keys (ORDER_B).
	idxA := strings.Index(out, "ORDER_A")
	idxB := strings.Index(out, "ORDER_B")
	if idxA < 0 {
		t.Fatal("ORDER_A not found in capture output")
	}
	if idxB < 0 {
		t.Fatal("ORDER_B not found in capture output")
	}
	if idxA >= idxB {
		t.Errorf("ORDER_A (at %d) should appear before ORDER_B (at %d)", idxA, idxB)
	}
}

func TestSessionStartupKeys(t *testing.T) {
	keys := []string{"echo SESSION_MARKER", "Enter"}
	sess := newTestSessionWithKeys(t, keys)

	got := sess.StartupKeys()
	if len(got) != len(keys) {
		t.Fatalf("StartupKeys() len = %d, want %d", len(got), len(keys))
	}
	for i := range keys {
		if got[i] != keys[i] {
			t.Errorf("StartupKeys()[%d] = %q, want %q", i, got[i], keys[i])
		}
	}
}

func TestSessionStartupKeysDefensiveCopy(t *testing.T) {
	keys := []string{"echo ORIGINAL", "Enter"}
	sess := newTestSessionWithKeys(t, keys)

	got := sess.StartupKeys()
	got[0] = "MUTATED"

	got2 := sess.StartupKeys()
	if got2[0] != "echo ORIGINAL" {
		t.Errorf("StartupKeys() was mutated: got %q, want %q", got2[0], "echo ORIGINAL")
	}
}

func TestWindowStartupKeys(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	keys := []string{"echo WIN_KEY", "Enter"}
	w, err := sess.NewWindow(ctx, "keyed", keys)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	got := w.StartupKeys()
	if len(got) != len(keys) {
		t.Fatalf("StartupKeys() len = %d, want %d", len(got), len(keys))
	}
	for i := range keys {
		if got[i] != keys[i] {
			t.Errorf("StartupKeys()[%d] = %q, want %q", i, got[i], keys[i])
		}
	}
}

func TestWindowStartupKeysDefensiveCopy(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	keys := []string{"echo ORIGINAL", "Enter"}
	w, err := sess.NewWindow(ctx, "keyed", keys)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	got := w.StartupKeys()
	got[0] = "MUTATED"

	got2 := w.StartupKeys()
	if got2[0] != "echo ORIGINAL" {
		t.Errorf("StartupKeys() was mutated: got %q, want %q", got2[0], "echo ORIGINAL")
	}
}

func TestWindowStartupKeysViaList(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	keys := []string{"echo WIN_KEY", "Enter"}
	w, err := sess.NewWindow(ctx, "keyed", keys)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	// Re-acquire via GetById.
	w2, err := sess.GetById(ctx, w.Id())
	if err != nil {
		t.Fatalf("GetById: %v", err)
	}

	got := w2.StartupKeys()
	if len(got) != len(keys) {
		t.Fatalf("StartupKeys() len = %d, want %d", len(got), len(keys))
	}
	for i := range keys {
		if got[i] != keys[i] {
			t.Errorf("StartupKeys()[%d] = %q, want %q", i, got[i], keys[i])
		}
	}
}

func TestInitialWindowStartupKeysEmpty(t *testing.T) {
	sess := newTestSession(t)
	ctx := context.Background()

	w, err := sess.GetAt(ctx, 0)
	if err != nil {
		t.Fatalf("GetAt(0): %v", err)
	}

	got := w.StartupKeys()
	if len(got) != 0 {
		t.Errorf("initial window StartupKeys() = %v, want empty", got)
	}
}

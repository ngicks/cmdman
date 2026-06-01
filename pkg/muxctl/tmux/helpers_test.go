package tmux_test

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/muxctl"
	tmuxctl "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// requireTmux fails (not skips) when tmux is missing: tmux is a real test
// dependency of this package, not an optional extra.
func requireTmux(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("tmux not in PATH: %v", err)
	}
	return p
}

// uniqueSocket derives a tmux socket name from the test name so parallel
// tests do not collide on a shared server.
func uniqueSocket(t *testing.T) string {
	t.Helper()
	return "muxctl-" + strings.ReplaceAll(t.Name(), "/", "_")
}

// run shells out to tmux on the given socket and returns stdout.
func run(t *testing.T, socket string, args ...string) string {
	t.Helper()
	full := append([]string{"-L", socket}, args...)
	cmd := exec.Command(requireTmux(t), full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v: %s", strings.Join(full, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// killServer tears down the per-test tmux server.
func killServer(t *testing.T, socket string) {
	t.Helper()
	cmd := exec.Command(requireTmux(t), "-L", socket, "kill-server")
	_ = cmd.Run()
}

// newSession constructs a Session against a fresh per-test tmux server and
// registers cleanup to kill the server when the test ends. It wires the
// ctrl-p,ctrl-q detach sequence (in tmux send-keys syntax) so tests exercising
// the graceful-detach path on re-apply behave like the real cmdman caller.
func newSession(t *testing.T, windowName string) (*tmuxctl.Session, string) {
	t.Helper()
	socket := uniqueSocket(t)
	t.Cleanup(func() { killServer(t, socket) })
	sess, err := tmuxctl.New(context.Background(), tmuxctl.Config{
		Socket:           socket,
		SessionName:      "cmdman-test",
		WindowName:       windowName,
		ViewerDetachKeys: []string{"C-p", "C-q"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sess, socket
}

// listPaneIDs returns the pane IDs in tmux's list order for windowID.
func listPaneIDs(t *testing.T, socket, windowID string) []string {
	t.Helper()
	out := run(t, socket, "list-panes", "-t", windowID, "-F", "#{pane_id}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// listPaneTitles returns the pane titles in tmux's list order for windowID.
func listPaneTitles(t *testing.T, socket, windowID string) []string {
	t.Helper()
	out := run(t, socket, "list-panes", "-t", windowID, "-F", "#{pane_title}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// listPaneMarkers returns each pane's @cmdman_marker option (empty when
// unset) in tmux's list order for windowID.
func listPaneMarkers(t *testing.T, socket, windowID string) []string {
	t.Helper()
	out := run(t, socket, "list-panes", "-t", windowID, "-F", "#{@cmdman_marker}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// listWindowNames returns every window name in the session.
func listWindowNames(t *testing.T, socket, sessionName string) []string {
	t.Helper()
	out := run(t, socket, "list-windows", "-t", sessionName, "-F", "#{window_name}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// sortedKeys returns the keys of panes in sorted order, for deterministic
// comparison in assertions.
func sortedKeys(panes map[string]muxctl.Pane) []string {
	keys := slices.Collect(maps.Keys(panes))
	slices.Sort(keys)
	return keys
}

// waitForMarker polls StatWindow until the window's marker equals want or the
// deadline passes (after which the test fails).
func waitForMarker(t *testing.T, sess *tmuxctl.Session, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		stat, err := sess.StatWindow(context.Background(), sess.WindowID())
		if err != nil {
			t.Fatalf("StatWindow: %v", err)
		}
		last = stat.Marker
		if last == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("marker never reached %d (last %d)", want, last)
}

// tempPath returns a path named name inside a fresh per-test temp dir.
func tempPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// waitForFile polls until path exists or the deadline passes; it reports
// whether the file appeared.
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err := os.Stat(path)
	return err == nil
}

// loadLayout reads a YAML fixture from testdata/, decodes + validates it,
// and returns the named layout's root pane. If layoutName is "", the first
// layout in document order is used.
func loadLayout(t *testing.T, file, layoutName string) muxctl.PaneSpec {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", file))
	if err != nil {
		t.Fatalf("open testdata/%s: %v", file, err)
	}
	defer f.Close()
	spec, err := muxctl.Decode(f)
	if err != nil {
		t.Fatalf("decode testdata/%s: %v", file, err)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("validate testdata/%s: %v", file, err)
	}
	for _, l := range spec.Layouts {
		if layoutName == "" || l.Name == layoutName {
			return l.Root
		}
	}
	t.Fatalf("layout %q not found in testdata/%s", layoutName, file)
	return muxctl.PaneSpec{}
}

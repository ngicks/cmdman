package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fixturePath resolves a path under pkg/muxctl/tmux/testdata/ from the
// tester package's directory.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// muxctltester → cmd → internal → muxctl → tmux/testdata
	return filepath.Join(cwd, "..", "..", "..", "tmux", "testdata", name)
}

// buildTester compiles the muxctltester binary into a temp dir and returns
// its absolute path. Pre-building (vs. `go run` from inside the pane) keeps
// the in-pane window of "between respawn and title-set" predictable.
func buildTester(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatalf("go not in PATH: %v", err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "muxctltester")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	return bin
}

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("tmux not in PATH: %v", err)
	}
}

func runTmux(t *testing.T, socket string, args ...string) string {
	t.Helper()
	full := append([]string{"-L", socket}, args...)
	cmd := exec.Command("tmux", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v: %s",
			strings.Join(full, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// startServer creates a per-test tmux server on a unique socket with one
// session "sm" whose initial pane runs /bin/sh (so we can send-keys
// commands into it). The server is killed at end of test.
func startServer(t *testing.T) string {
	t.Helper()
	socket := "mxtester-" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	runTmux(t, socket,
		"new-session", "-d", "-s", "sm", "-x", "200", "-y", "50", "/bin/sh")
	return socket
}

// waitForAllPaneTitlesParseable polls the pane titles in window sm:0 until
// every line has a "#<digits>" suffix, or until timeout — in which case it
// returns the last observed titles so the test can fail with context.
func waitForAllPaneTitlesParseable(
	t *testing.T,
	socket string,
	timeout time.Duration,
) ([]string, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastTitles []string
	for time.Now().Before(deadline) {
		out := runTmux(t, socket, "list-panes", "-t", "sm:0", "-F", "#{pane_title}")
		lastTitles = nil
		if out != "" {
			lastTitles = strings.Split(out, "\n")
		}
		allMarked := len(lastTitles) > 0
		for _, line := range lastTitles {
			if !markerSuffix.MatchString(line) {
				allMarked = false
				break
			}
		}
		if allMarked {
			return lastTitles, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return lastTitles, errors.New("timeout waiting for all pane titles to carry a marker suffix")
}

// TestApplyInPane_PersistsMarkerOnFirstRun reproduces the user-reported
// bug: when the tester is invoked from inside a single-pane tmux window
// (the "single-pane fast path" → reuse), the apply must still leave the
// pane-border title carrying the "#<marker>" suffix so subsequent runs
// can read it back and cycle.
//
// Reproduction strategy: pre-build the tester, send-keys its invocation
// into a tmux pane running /bin/sh, then wait for the pane titles to
// carry the marker suffix. Pre-fix: the respawn-pane kills the tester's
// process group before select-pane -T runs, the title never gets the
// suffix, and the wait times out.
func TestApplyInPane_PersistsMarkerOnFirstRun(t *testing.T) {
	requireTmux(t)
	bin := buildTester(t)
	fixture := fixturePath(t, "single-leaf.yaml")
	socket := startServer(t)

	// Tell the shell in the pane to run the tester and write a sentinel
	// line on completion so timeout vs success is distinguishable. The
	// sentinel is best-effort — if the tester dies during respawn it
	// never runs, and the test relies on the title-polling timeout to
	// surface the bug.
	cmdLine := bin + " " + fixture + "; echo TESTER_DONE"
	runTmux(t, socket, "send-keys", "-t", "sm:0.0", cmdLine, "Enter")

	titles, err := waitForAllPaneTitlesParseable(t, socket, 5*time.Second)
	if err != nil {
		t.Fatalf("first-run pane titles never carried a marker suffix; last titles=%v: %v",
			titles, err)
	}
	if !slices.Equal(titles, []string{"only#0"}) {
		t.Errorf("titles = %v, want [only#0]", titles)
	}
}

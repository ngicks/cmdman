package cmdman_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// These tests cover `cmdman compose up --mux`: the one-shot bring-up that opens
// the project's mux dashboard right after up succeeds, reusing the spec up
// already loaded. Like the other mux e2e tests they drive a tmux server on a
// dedicated -L socket and run the binary with $TMUX stripped (muxExec), so the
// driver takes the outside-a-multiplexer path.

// composeUpMuxYAML is a compose file with two long-running services and two mux
// layouts of different shape: "wide" (both services side by side) and "solo"
// (alpha alone). Two layouts make the pinned-layout behavior observable — a
// tail that cycled like `compose mux up` would land on "solo" (marker 1, one
// pane) on the second run.
func composeUpMuxYAML(project, socket string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, "300"]
  beta:
    args: [sleep, "300"]
mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: wide
      root:
        dir: h
        splits: [1, 1]
        panes: [alpha, beta]
    - name: solo
      root:
        command: alpha
`, project, socket)
}

// TestComposeUpMux_BuildsDashboardAndIsIdempotent runs `compose up --mux` twice
// against the same project. The first run must bring the services up and build
// the project-named window (cmdman-<project>) with one pane per service; the
// second run must reconcile onto the same window with the same layout — same
// window id, same panes, and the layout marker still 0 (the tail pins the first
// layout instead of cycling).
func TestComposeUpMux_BuildsDashboardAndIsIdempotent(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "upmux"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(t, wd, composeUpMuxYAML(project, socket))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "up", "--mux",
	)
	if err != nil {
		t.Fatalf("compose up --mux failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	// The mux tail ran: outside tmux it prints the attach hint on stdout.
	if !strings.Contains(stdout, "tmux attach -t cmdman") {
		t.Fatalf("expected the mux attach hint on stdout; got:\n%s", stdout)
	}

	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", defaultTimeout)
	}

	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)
	// Unpinned compose leaves resolve at replica position 1, so panes are titled
	// <command>-1.
	want := []string{"alpha-1", "beta-1"}
	if got := windowPaneBases(t, socket, wid); !slices.Equal(got, want) {
		t.Fatalf("pane base names = %v, want %v", got, want)
	}
	if got := windowMarker(t, socket, wid); got != 0 {
		t.Fatalf("after first run marker = %d, want 0 (first layout)", got)
	}

	// Re-run: reconcile, not a second dashboard and not a layout cycle.
	stdout, stderr, err = env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "up", "--mux",
	)
	if err != nil {
		t.Fatalf(
			"second compose up --mux failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout, stderr,
		)
	}
	if got := tmuxWindowID(t, socket, window); got != wid {
		t.Fatalf(
			"window id drifted across runs: %s vs %s (should reuse the owned window)",
			wid,
			got,
		)
	}
	if got := windowPaneBases(t, socket, wid); !slices.Equal(got, want) {
		t.Fatalf("after second run pane base names = %v, want %v", got, want)
	}
	if got := windowMarker(t, socket, wid); got != 0 {
		t.Fatalf("after second run marker = %d, want 0 (--mux pins the first layout)", got)
	}
}

// TestComposeUpMux_NoMuxSectionWarnsAndSucceeds verifies the deliberate
// divergence from `compose mux up` (which hard-errors on a missing section):
// with --mux, a project whose compose file has no "mux:" section still comes up
// successfully and only the dashboard is skipped, with a warning on stderr.
func TestComposeUpMux_NoMuxSectionWarnsAndSucceeds(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "upmuxless"
	// Long-running commands: the assertion is about the warning and the up, not
	// about a command's exit, so staying "running" keeps the start reconcile off
	// any exit-race timing.
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, "300"]
  beta:
    args: [sleep, "300"]
`, project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	stdout, stderr, err := env.exec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "up", "--mux",
	)
	if err != nil {
		t.Fatalf("compose up --mux failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "mux:") || !strings.Contains(stderr, "skipped") {
		t.Fatalf("expected a skipped-mux warning on stderr; got stderr:\n%s", stderr)
	}

	// The up itself still happened.
	if got := len(env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)); got != 2 {
		t.Fatalf("compose commands created = %d, want 2", got)
	}
}

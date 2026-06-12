package cmdman_test

// e2e tests for `compose mux cycle-scale` and related cycle-scale features.
// These tests require a real tmux binary (guarded by requireTmux).
//
// Scenarios covered:
//  1. cycle-scale happy path: up → cycle-scale web → web-2; cycle-scale web=3 → web-3;
//     wrap back to web-1.
//  2. Layout cycle (compose mux up again) after cycle-scale keeps the replica position.
//  3. cycle-scale with no dashboard window → error mentioning "compose mux up".
//  4. compose mux ls shows the SCALE column: web=1/3 after up, web=2/3 after
//     cycle-scale web.
//  5. compose mux down then up → position reset to 1.
//  6. Static validation: compose file with mux: leaf scale: 4 against commands:
//     scale: 2 fails to load with the mux validation error.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// composeMuxCycleScaleYAML returns a compose file with a scaled `web` service
// (scale: 3) and a one-layout mux: section whose single leaf is unpinned (no
// scale: in the mux: section), making it a cycle-scale target.
//
// The spec uses a custom tmux socket so the test server is isolated.
func composeMuxCycleScaleYAML(project, socket string) string {
	return fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
    scale: 3
mux:
  driver: tmux
  driver_opt:
    socket: %s
  layouts:
    - name: main
      root:
        command: web
`, project, socket)
}

// composeMuxTwoLayoutsCycleScaleYAML is like composeMuxCycleScaleYAML but with
// two layouts so the layout-cycle test can switch between them.
func composeMuxTwoLayoutsCycleScaleYAML(project, socket string) string {
	return fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
    scale: 3
  worker:
    args: [sleep, "300"]
mux:
  driver: tmux
  driver_opt:
    socket: %s
  layouts:
    - name: wide
      root:
        dir: h
        splits: [1, 1]
        panes:
          - web
          - worker
    - name: solo
      root:
        command: web
`, project, socket)
}

// waitForPaneTitle polls the pane titles of windowID until any pane carries the
// expected title, or the deadline is reached. It returns the titles observed on
// success; on timeout it calls t.Fatalf.
func waitForPaneTitle(
	t *testing.T,
	socket, windowID, wantTitle string,
	timeout time.Duration,
) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []string
	for time.Now().Before(deadline) {
		last = tmuxPaneField(t, socket, windowID, "#{pane_title}")
		if slices.Contains(last, wantTitle) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf(
		"timed out waiting for pane title %q in window %s; last titles: %v",
		wantTitle, windowID, last,
	)
	return nil
}

// TestComposeMuxCycleScale_HappyPath is the primary cycle-scale e2e test.
//
// Setup: a compose project with `web` scaled to 3 replicas; the mux: spec has
// a single-pane layout with an unpinned `web` leaf.
//
// Sequence:
//  1. compose mux up → pane title = "web-1".
//  2. compose mux cycle-scale web → pane title = "web-2"; stdout includes
//     "<session>:<window> web -> web-2".
//  3. compose mux cycle-scale web=3 → pane title = "web-3"; stdout includes
//     "<session>:<window> web -> web-3".
//  4. compose mux cycle-scale web (wrap) → pane title = "web-1".
func TestComposeMuxCycleScale_HappyPath(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "cs-happy"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(t, wd, composeMuxCycleScaleYAML(project, socket))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// Bring web replicas up.
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

	// Step 1: compose mux up — pane shows replica 1.
	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)

	// Pane title should be "web-1" (replica 1 on first up).
	waitForPaneTitle(t, socket, wid, "web-1", 3*time.Second)

	// Step 2: cycle-scale web → advance to replica 2.
	stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	)
	if err != nil {
		t.Fatalf("cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	// stdout: "<session>:<window> web -> web-2"
	if !strings.Contains(stdout, "web -> web-2") {
		t.Fatalf("expected 'web -> web-2' in output; got:\n%s", stdout)
	}
	// Pane title reflects the new replica.
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)

	// Step 3: cycle-scale web=3 → jump to replica 3.
	stdout, stderr, err = env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web=3",
	)
	if err != nil {
		t.Fatalf("cycle-scale web=3 failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "web -> web-3") {
		t.Fatalf("expected 'web -> web-3' in output; got:\n%s", stdout)
	}
	waitForPaneTitle(t, socket, wid, "web-3", 3*time.Second)

	// Step 4: cycle-scale web again — wraps back to replica 1 (3+1 % 3 = 1).
	stdout, stderr, err = env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	)
	if err != nil {
		t.Fatalf("cycle-scale web (wrap) failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "web -> web-1") {
		t.Fatalf("expected 'web -> web-1' in output (wrap); got:\n%s", stdout)
	}
	waitForPaneTitle(t, socket, wid, "web-1", 3*time.Second)
}

// TestComposeMuxCycleScale_PersistsAcrossLayoutCycle verifies that the replica
// position set by cycle-scale is preserved across a layout switch (compose mux
// up cycles to the next layout). After cycle-scale web (replica 2), re-running
// compose mux up applies the next layout and the pane title stays at "web-2".
func TestComposeMuxCycleScale_PersistsAcrossLayoutCycle(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "cs-persist"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(
		t, wd, composeMuxTwoLayoutsCycleScaleYAML(project, socket),
	)
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// Bring services up.
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

	// First compose mux up → layout "wide" (two panes), web-1.
	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up (1) failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)
	if got := windowMarker(t, socket, wid); got != 0 {
		t.Fatalf("after first up marker = %d, want 0", got)
	}
	waitForPaneTitle(t, socket, wid, "web-1", 3*time.Second)

	// Advance web to replica 2.
	if stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	); err != nil {
		t.Fatalf("cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)

	// Second compose mux up → cycles to layout "solo" (marker 1), single pane.
	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up (2) failed: %v\nstderr:\n%s", err, stderr)
	}
	if got := windowMarker(t, socket, wid); got != 1 {
		t.Fatalf("after second up marker = %d, want 1", got)
	}
	// The position (replica 2) must be preserved: "solo" layout has an unpinned
	// web leaf and the stored position is 2.
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)
}

// TestComposeMuxCycleScale_NoWindowError verifies that `compose mux cycle-scale`
// without a running dashboard returns an error that mentions "compose mux up".
func TestComposeMuxCycleScale_NoWindowError(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "cs-nowin"
	socket := muxSocket(t)
	// No cleanup needed: we never start a tmux server in this test.

	composePath := writeComposeFile(t, wd, composeMuxCycleScaleYAML(project, socket))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// Bring web replicas up but do NOT run compose mux up.
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

	// cycle-scale must fail with an error mentioning "compose mux up".
	stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	)
	if err == nil {
		t.Fatalf(
			"cycle-scale without dashboard should fail; stdout=%q stderr=%q",
			stdout, stderr,
		)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "compose mux up") {
		t.Fatalf(
			"expected error mentioning 'compose mux up'; got stdout=%q stderr=%q",
			stdout, stderr,
		)
	}
}

// TestComposeMuxLs_ShowsScaleColumn verifies that `compose mux ls` displays the
// SCALE column with the correct `web=1/3` after up and `web=2/3` after
// cycle-scale web.
func TestComposeMuxLs_ShowsScaleColumn(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	// Use the default tmux socket (isolated via TMUX_TMPDIR) so both
	// `compose mux` and `compose mux ls` hit the same server without requiring
	// driver_opt.socket in the YAML (ls resolves driver_opt from the compose spec).
	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	wd := composeWorkdir(t)
	project := "cs-ls"
	// No driver_opt.socket: uses the default socket redirected via TMUX_TMPDIR.
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
    scale: 3
mux:
  driver: tmux
  layouts:
    - name: main
      root:
        command: web
`, project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// Bring web replicas up.
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

	// compose mux up.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up failed: %v\nstderr:\n%s", err, stderr)
	}

	// compose mux ls: SCALE column should show web=1/3.
	stdout, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"compose", "--workdir", wd, "-f", composePath,
		"mux", "ls", "--format", "{{.Scale}}",
	)
	if err != nil {
		t.Fatalf(
			"compose mux ls (after up) failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout, stderr,
		)
	}
	if !strings.Contains(stdout, "web=1/3") {
		t.Fatalf("expected 'web=1/3' in ls output after up; got:\n%s", stdout)
	}

	// cycle-scale web → advance to replica 2.
	if stdout2, stderr2, err2 := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	); err2 != nil {
		t.Fatalf(
			"cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s",
			err2, stdout2, stderr2,
		)
	}

	// compose mux ls: SCALE column should now show web=2/3.
	stdout, stderr, err = env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"compose", "--workdir", wd, "-f", composePath,
		"mux", "ls", "--format", "{{.Scale}}",
	)
	if err != nil {
		t.Fatalf(
			"compose mux ls (after cycle-scale) failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout, stderr,
		)
	}
	if !strings.Contains(stdout, "web=2/3") {
		t.Fatalf("expected 'web=2/3' in ls output after cycle-scale; got:\n%s", stdout)
	}
}

// TestComposeMuxCycleScale_DownResetsPosition verifies that after cycle-scale
// advances the replica position, `compose mux down` clears the position so the
// next `compose mux up` starts at replica 1 again.
func TestComposeMuxCycleScale_DownResetsPosition(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "cs-down"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(t, wd, composeMuxCycleScaleYAML(project, socket))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// Bring web replicas up.
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

	// First compose mux up.
	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up (1) failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)
	waitForPaneTitle(t, socket, wid, "web-1", 3*time.Second)

	// Advance to replica 2.
	if stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	); err != nil {
		t.Fatalf(
			"cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr,
		)
	}
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)

	// Verify @cmdman_scale is set before down.
	if got := tmuxWindowOption(t, socket, wid, "@cmdman_scale"); !strings.Contains(got, "web=2") {
		t.Fatalf("expected @cmdman_scale to contain 'web=2' before down; got: %q", got)
	}

	// compose mux down.
	if downStdout, downStderr, downErr := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "down",
	); downErr != nil {
		t.Fatalf(
			"compose mux down failed: %v\nstdout:\n%s\nstderr:\n%s",
			downErr, downStdout, downStderr,
		)
	}

	// The @cmdman_scale option should be cleared after down.
	if got := tmuxWindowOption(t, socket, wid, "@cmdman_scale"); got != "" {
		t.Errorf("@cmdman_scale still set after down: %q", got)
	}

	// Second compose mux up — position must reset to 1.
	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up (2) failed: %v\nstderr:\n%s", err, stderr)
	}
	// Window id may have changed if up restored the window to a fresh state.
	// Re-lookup the window id.
	wid2 := tmuxWindowID(t, socket, window)
	waitForPaneTitle(t, socket, wid2, "web-1", 3*time.Second)
}

// TestComposeMux_MuxValidationScaleExceedsCommand verifies that loading a
// compose file whose mux: section declares a pinned leaf scale that exceeds the
// command's scale fails with the static validation error from validateMux.
//
// This exercises scenario 6 (static validation) without tmux — the error fires
// at normalize / load time before any tmux interaction.
func TestComposeMux_MuxValidationScaleExceedsCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "cs-validate"
	// commands.web has scale: 2 but the mux: leaf pins scale: 4 — should fail.
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
    scale: 2
mux:
  driver: tmux
  layouts:
    - name: main
      root:
        command: web
        scale: 4
`, project))
	// No compose up — we expect load to fail.

	// Any compose mux subcommand that parses the spec should fail at validation.
	stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	)
	if err == nil {
		t.Fatalf(
			"expected validation error for scale 4 > commands.web.scale 2; "+
				"stdout=%q stderr=%q",
			stdout, stderr,
		)
	}
	combined := stdout + "\n" + stderr
	// Error string from validateMuxPane:
	//   "mux: layout %q: leaf %q: scale %d exceeds commands.%s.scale %d"
	if !strings.Contains(combined, "scale") || !strings.Contains(combined, "exceeds") {
		t.Fatalf(
			"expected 'scale ... exceeds ...' in error output; got stdout=%q stderr=%q",
			stdout, stderr,
		)
	}
	if !strings.Contains(combined, "web") {
		t.Fatalf("expected 'web' in validation error; got stdout=%q stderr=%q", stdout, stderr)
	}
}

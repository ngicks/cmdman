package cmdman_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/compose"
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
  driver:
    name: tmux
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
  driver:
    name: tmux
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

	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)

	waitForPaneTitle(t, socket, wid, "web-1", 3*time.Second)

	stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	)
	if err != nil {
		t.Fatalf("cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "web -> web-2") {
		t.Fatalf("expected 'web -> web-2' in output; got:\n%s", stdout)
	}
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)

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

	if stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	); err != nil {
		t.Fatalf("cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)

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
	composePath := writeComposeFile(t, wd, composeMuxCycleScaleYAML(project, socket))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

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
  driver:
    name: tmux
  layouts:
    - name: main
      root:
        command: web
`, project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

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

	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up failed: %v\nstderr:\n%s", err, stderr)
	}

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

	if stdout2, stderr2, err2 := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	); err2 != nil {
		t.Fatalf(
			"cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s",
			err2, stdout2, stderr2,
		)
	}

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

	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up (1) failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)
	waitForPaneTitle(t, socket, wid, "web-1", 3*time.Second)

	if stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	); err != nil {
		t.Fatalf(
			"cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr,
		)
	}
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)

	if got := tmuxWindowOption(t, socket, wid, "@cmdman_scale"); !strings.Contains(got, "web=2") {
		t.Fatalf("expected @cmdman_scale to contain 'web=2' before down; got: %q", got)
	}

	if downStdout, downStderr, downErr := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "down",
	); downErr != nil {
		t.Fatalf(
			"compose mux down failed: %v\nstdout:\n%s\nstderr:\n%s",
			downErr, downStdout, downStderr,
		)
	}

	if got := tmuxWindowOption(t, socket, wid, "@cmdman_scale"); got != "" {
		t.Errorf("@cmdman_scale still set after down: %q", got)
	}

	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up (2) failed: %v\nstderr:\n%s", err, stderr)
	}
	// The window down handed back is the user's again — it carries no project —
	// so this up builds one of its own, beside it and under the same name. The
	// ownership stamp is what tells the two apart.
	wid2 := tmuxWindowIDByIdentity(
		t, socket,
		compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity(),
	)
	if wid2 == wid {
		t.Fatalf("up after down landed back in the restored window %s", wid)
	}
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
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
    scale: 2
mux:
  driver:
    name: tmux
  layouts:
    - name: main
      root:
        command: web
        scale: 4
`, project))
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

// TestComposeMuxCycleScale_SurvivesFloatingPane covers the dashboard window
// hosting a pane cmdman never stamped: tmux's own floating pane (tmux 3.7,
// pane_floating_flag), which is what a project-manager summoned from a key
// binding runs in. The window must still read as its layout, cycle-scale must
// still find and respawn the leaf, and a layout cycle must rebuild the tiled
// region without killing the floating pane.
func TestComposeMuxCycleScale_SurvivesFloatingPane(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "cs-float"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(
		t, wd, composeMuxTwoLayoutsCycleScaleYAML(project, socket),
	)
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

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

	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up (1) failed: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowID(t, socket, "cmdman-"+project)
	waitForPaneTitle(t, socket, wid, "web-1", 3*time.Second)

	floatID, flag, _ := strings.Cut(tmuxRun(
		t, socket, "new-pane", "-d", "-t", wid,
		"-P", "-F", "#{pane_id}\t#{pane_floating_flag}",
	), "\t")
	if flag != "1" {
		t.Skipf("this tmux has no floating panes (new-pane gave flag %q)", flag)
	}

	// cycle-scale reads the window's marker first; the unstamped floating pane
	// must not make the window read as unmarked.
	stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	)
	if err != nil {
		t.Fatalf("cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "web -> web-2") {
		t.Fatalf("expected 'web -> web-2' in output; got:\n%s", stdout)
	}
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)

	// A layout cycle resets the tiled region; the floating pane is not part of
	// it and must come through alive.
	if _, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux",
	); err != nil {
		t.Fatalf("compose mux up (2) failed: %v\nstderr:\n%s", err, stderr)
	}
	waitForPaneTitle(t, socket, wid, "web-2", 3*time.Second)
	if got := tiledWindowMarker(t, socket, wid); got != 1 {
		t.Fatalf("after layout cycle marker = %d, want 1", got)
	}
	ids := tmuxPaneField(t, socket, wid, "#{pane_id}")
	if !slices.Contains(ids, floatID) {
		t.Fatalf("floating pane %s was killed by the layout cycle; panes now %v", floatID, ids)
	}
}

// tiledWindowMarker is windowMarker over the tiled panes only: a floating pane
// carries no marker and is not part of the layout the marker indexes.
func tiledWindowMarker(t *testing.T, socket, windowID string) int {
	t.Helper()
	marker := -2
	for _, line := range tmuxPaneField(
		t, socket, windowID, "#{pane_floating_flag}\t#{@cmdman_marker}",
	) {
		floating, v, _ := strings.Cut(line, "\t")
		if floating == "1" {
			continue
		}
		m := -1
		if v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("non-numeric @cmdman_marker %q", v)
			}
			m = n
		}
		if marker == -2 {
			marker = m
			continue
		}
		if m != marker {
			t.Fatalf("inconsistent layout markers across tiled panes of %s", windowID)
		}
	}
	if marker == -2 {
		t.Fatalf("window %s has no tiled panes", windowID)
	}
	return marker
}

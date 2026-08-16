package cmdman_test

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// muxlessEnv is the process environment with $TMUX and $ZELLIJ stripped, the
// way muxExec builds the environment it runs the CLI under: a suite run from
// inside tmux would otherwise let the widget's enclosing-window probe reach the
// developer's own server.
//
// TMUX_TMPDIR is deliberately left alone. tmux resolves a -L socket name under
// it, so redirecting it — as tmuxTmpdirEnv does for the tests that need the
// default socket — would point the widget at a different socket path than the
// dashboard the fixture built on its dedicated one.
func muxlessEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(s string) bool {
		return strings.HasPrefix(s, "TMUX=") || strings.HasPrefix(s, "ZELLIJ=")
	})
}

// The project-manager widget against live projects (PLAN step 8): the token
// probe standing alone, the probe trail when nothing answers, and the two
// actions whose effect lives outside the process — the replica count in the
// store and the shown replica in the dashboard's window state.

// TestTUIWidget_ProjectManagerResolvesByToken is D10 with every other probe
// taken away: the panel runs in an empty directory, with $TMUX stripped so the
// enclosing-window probe is not even asked (D13), and the only thing that says
// which project it manages is the --mux-token it was handed.
func TestTUIWidget_ProjectManagerResolvesByToken(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	// The dashboard goes on tmux's default socket, kept private by TMUX_TMPDIR,
	// so the widget's driver autodetection reaches the same server.
	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	const project, service = "pmtoken", "tokensvc"
	wd := composeWorkdir(t)
	composePath := writeComposeFile(t, wd, summonMuxYAML(project, service, 3))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "up", "--mux",
	); err != nil {
		t.Fatalf("compose up --mux failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	waitForTmuxWindow(t, tmuxTmpdir, window, 30*time.Second)
	wid := tmuxWindowIDTmpdir(t, tmuxTmpdir, window)

	// Unlike the enclosing-window probe, this one is not client-relative: the
	// token is matched against the listed windows, so no client is attached and
	// the dashboard is left where `up --mux` put it — in the background.
	elsewhere := t.TempDir()
	w := startWidgetEnv(t, ctx, env, elsewhere, elsewhere, "project-manager",
		tmuxTmpdirEnv(tmuxTmpdir), "--mux-token", wid)

	// The head names the project the token resolved to, and the service row is
	// that project's own — neither string is reachable from where the process
	// stands.
	w.waitFor(t, project, 20*time.Second)
	w.waitFor(t, service, 20*time.Second)
	// Both of those come off the spec, which loads the same whatever directory
	// the panel stands in. The count does not: it is the project's own stored
	// commands, so ×3 is what proves the load carried the resolved project's
	// work directory rather than falling back to the panel's own.
	w.waitFor(t, "×3", 20*time.Second)
	if snap := w.snapshot(); strings.Contains(snap, "no project") {
		t.Errorf("the token probe should have answered; got:\n%q", snap)
	}
	w.quit(t)
}

// TestTUIWidget_ProjectManagerBogusTokenNamesEveryProbe is D4 as amended by
// D10: with a token that matches no window, no multiplexer around the process
// and no project in its directory, the body is the trail of every probe that
// was asked rather than the last failure alone.
func TestTUIWidget_ProjectManagerBogusTokenNamesEveryProbe(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)

	// A TMUX_TMPDIR of its own with no server under it, so the token cannot
	// match a window anywhere and no developer session is reachable either.
	elsewhere := t.TempDir()
	w := startWidgetEnv(t, ctx, env, elsewhere, elsewhere, "project-manager",
		tmuxTmpdirEnv(t.TempDir()), "--mux-token", "@999")

	// Single tokens, not phrases: the trail is word-wrapped into the popup
	// width, so "enclosing window" can straddle a line break while the words it
	// is made of cannot. The reasons themselves are left out — "no server" and
	// "matches no cmdman-owned window" are both honest answers here, and which
	// one comes back is tmux's business.
	for _, want := range []string{`"@999"`, "enclosing", "directory"} {
		w.waitFor(t, want, 15*time.Second)
	}
	w.quit(t)
}

// TestTUIWidget_ProjectManagerScalesService drives `+` on a service row against
// a live project: the key has to reach the same compose scale path
// `cmdman compose scale` runs, so the new replica exists in the store and not
// only on the screen.
func TestTUIWidget_ProjectManagerScalesService(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	const project = "pmscale"
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

	// The panel stands in the project's own directory: the cwd probe is what
	// resolves it, and the scale it runs loads the compose file from there too.
	w := startWidgetEnv(t, ctx, env, wd, wd, "project-manager", muxlessEnv())

	// The count has to be on screen before the key is sent: a key that arrives
	// while the list is still loading selects no row and does nothing.
	w.waitFor(t, "×3", 20*time.Second)
	w.send(t, "+")
	// The panel's own report that the backend call returned, which is also what
	// tells the reload below apart from the load before the key.
	w.waitFor(t, "web scaled to 4", 60*time.Second)
	// The count is redrawn in place, one cell of an otherwise unchanged row, so
	// the row only reaches the captured stream whole once the widget repaints.
	w.resize(t, 24, 80)
	w.waitFor(t, "×4", 20*time.Second)

	// The row is the panel's own reading; the store is what the scale actually
	// changed.
	entries := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(entries) != 4 {
		t.Fatalf("project holds %d replicas after +, want 4; panel:\n%q",
			len(entries), w.snapshot())
	}
	w.quit(t)
}

// TestTUIWidget_ProjectManagerCyclesShownReplica drives `l` on a cyclable row:
// the dashboard pane must move to the next replica and the position must land
// in the window state the panel reads back (@cmdman_scale), which is what makes
// the badge a report rather than a guess.
func TestTUIWidget_ProjectManagerCyclesShownReplica(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	const project = "pmcycle"
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
	wid := tmuxWindowID(t, socket, "cmdman-"+project)
	waitForPaneTitle(t, socket, wid, "web-1", 5*time.Second)

	w := startWidgetEnv(t, ctx, env, wd, wd, "project-manager", muxlessEnv())

	// Nothing has been cycled yet, so no window holds a position: the unknown
	// badge is both the loaded state and the proof the row is a cycle target.
	w.waitFor(t, "[?]", 20*time.Second)
	w.send(t, "l")

	waitForPaneTitle(t, socket, wid, "web-2", 20*time.Second)
	// The note is only drawn once the reload behind it has answered — while it
	// runs the footer says so instead — which makes it the signal that the badge
	// below has been rebuilt from the state the cycle just persisted. It is also
	// what makes the state readable: the pane is respawned before the position is
	// written (cmdman/mux/cycle_scale.go:237-252), so the title alone arrives
	// while @cmdman_scale is still unset.
	w.waitFor(t, "cycled shown replica of web", 20*time.Second)
	if got := tmuxWindowOption(t, socket, wid, "@cmdman_scale"); !strings.Contains(got, "web=2") {
		t.Fatalf("@cmdman_scale = %q, want it to carry web=2; panel:\n%q", got, w.snapshot())
	}
	w.resize(t, 24, 80)
	w.waitFor(t, "[2]", 20*time.Second)
	w.quit(t)
}

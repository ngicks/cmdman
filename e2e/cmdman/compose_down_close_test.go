package cmdman_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// `compose down --close-windows` takes the project's multiplexer windows with
// the commands, as the TUI's own down does. These tests read the windows off a
// real tmux server, because only the server can say whether a window is gone,
// restored, or untouched.
//
// Every down here runs against the default-socket server under a private
// TMUX_TMPDIR, so a window it wrongly closed is visible as an absence there and
// the developer's own tmux is never reached.

// TestComposeDownCloseWindows_ClosesProjectWindowsButSparesTheOneItRunsIn has
// two windows stamped for the project and runs the down from a pane of one of
// them. The other window is closed. The hosting window is restored instead,
// since closing it would SIGHUP the down before it finished.
//
// A pane hands its address to what it starts through $TMUX_PANE alone, so
// naming the dashboard's pane in the down's environment puts the down inside
// that window.
func TestComposeDownCloseWindows_ClosesProjectWindowsButSparesTheOneItRunsIn(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	// A session of the test's own, so the server outlives the project windows.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "keep", "-n", "home")

	wd := composeWorkdir(t)
	const project = "cdclosewin"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	composeCmd := tmpdirComposeCmd(env, wd, composePath, tmuxTmpdir)

	// Both dashboards are built from outside tmux, so cmdman opens each window
	// rather than taking one over: a taken-over window is restored for that
	// reason alone, which would hide the one under test.
	composeCmd("up", "--mux").Run(ctx, t)
	composeCmd("mux", "up", "-s", "keep").Run(ctx, t)
	identity := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()
	windows := waitForStampedWindows(t, tmuxTmpdir, identity, 2, 30*time.Second)
	hosting, other := windows[0], windows[1]
	panes := tmuxRunWithTmpdir(t, tmuxTmpdir, "list-panes", "-t", hosting, "-F", "#{pane_id}")
	hostingPane, _, _ := strings.Cut(panes, "\n")

	res := composeCmd("down", "--close-windows").
		WithEnv(
			"TMUX="+tmuxEnvValue(t, tmuxTmpdir, hosting),
			"TMUX_PANE="+hostingPane,
		).
		Exec(ctx)
	if res.Err != nil {
		t.Fatalf("compose down --close-windows failed: %v\nstderr:\n%s", res.Err, res.Stderr)
	}
	if strings.Contains(res.Stderr, "warning:") {
		t.Errorf("every window closed or restored, yet down warned:\n%s", res.Stderr)
	}

	ids := windowIDsTmpdir(t, tmuxTmpdir)
	if slices.Contains(ids, other) {
		t.Errorf("window %s is still on the server after the down; windows: %v", other, ids)
	}
	if !slices.Contains(ids, hosting) {
		t.Errorf("the window the down runs in (%s) was closed underneath it; windows: %v",
			hosting, ids)
	}
	// The hosting window was handed back, so nothing claims the project any more.
	waitForNoStampedWindow(t, tmuxTmpdir, identity, 10*time.Second)
	if left := composeCommandIDs(ctx, t, env, wd, project); len(left) != 0 {
		t.Errorf("commands left after the down: %v", left)
	}
}

// TestComposeDownCloseWindows_KeepsWindowsWhenStopPreFails is the down that did
// not get through. A stop_pre failing under on_error fail keeps its replica
// running, and a window that still has a running command to show stays.
func TestComposeDownCloseWindows_KeepsWindowsWhenStopPreFails(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "keep", "-n", "home")

	wd := composeWorkdir(t)
	const project = "cdclosefail"
	composePath := writeComposeFile(t, wd, refusingStopPreMuxYAML(project))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	composeCmd := tmpdirComposeCmd(env, wd, composePath, tmuxTmpdir)

	composeCmd("up", "--mux").Run(ctx, t)
	identity := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()
	dashboard := waitForStampedWindow(t, tmuxTmpdir, identity, 30*time.Second)

	composeCmd("down", "--close-windows").ExpectFail(ctx, t)

	// The premise: the down kept the replica the failed stop_pre guarded.
	if left := composeCommandIDs(ctx, t, env, wd, project); len(left) == 0 {
		t.Fatalf("the down removed every command, so the failed stop_pre kept nothing")
	}
	if got := windowsStampedTmpdir(t, tmuxTmpdir, identity); !slices.Equal(
		got, []string{dashboard},
	) {
		t.Errorf("windows stamped for the project after a failed down = %v, want [%s]",
			got, dashboard)
	}
}

// TestComposeDown_WithoutCloseWindowsKeepsWindows pins the default: a down that
// removed every command still leaves the windows alone unless asked, because a
// window that closes underneath a command line is a surprise.
func TestComposeDown_WithoutCloseWindowsKeepsWindows(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "keep", "-n", "home")

	wd := composeWorkdir(t)
	const project = "cdkeepwin"
	composePath := writeComposeFile(t, wd, launcherMuxYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	composeCmd := tmpdirComposeCmd(env, wd, composePath, tmuxTmpdir)

	composeCmd("up", "--mux").Run(ctx, t)
	identity := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()
	dashboard := waitForStampedWindow(t, tmuxTmpdir, identity, 30*time.Second)

	composeCmd("down").Run(ctx, t)

	if left := composeCommandIDs(ctx, t, env, wd, project); len(left) != 0 {
		t.Fatalf("commands left after the down: %v", left)
	}
	if got := windowsStampedTmpdir(t, tmuxTmpdir, identity); !slices.Equal(
		got, []string{dashboard},
	) {
		t.Errorf("windows stamped for the project after a plain down = %v, want [%s]",
			got, dashboard)
	}
}

// TestComposeDownCloseWindows_RejectsCommandNames covers the targeted down: it
// leaves the project's other commands running, so their windows are still in
// use, and --close-windows refuses it before any command is touched.
func TestComposeDownCloseWindows_RejectsCommandNames(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	const project = "cdclosenames"
	composePath := writeComposeFile(t, wd, launcherMuxlessYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	composeCmd := func(args ...string) *Cmd {
		return env.Cmd(append(
			[]string{"compose", "--workdir", wd, "-f", composePath}, args...)...,
		).Muxless()
	}

	composeCmd("create").Run(ctx, t)
	before := composeCommandIDs(ctx, t, env, wd, project)
	if len(before) == 0 {
		t.Fatal("compose create registered no command")
	}

	composeCmd("down", "--close-windows", "alpha").ExpectFail(ctx, t, "--close-windows")

	if after := composeCommandIDs(ctx, t, env, wd, project); !slices.Equal(after, before) {
		t.Errorf("commands after the refused down = %v, want %v untouched", after, before)
	}
}

// tmpdirComposeCmd builds `cmdman compose` invocations of the project in wd
// against the default-socket tmux server under tmuxTmpdir.
func tmpdirComposeCmd(env *testEnv, wd, composePath, tmuxTmpdir string) func(...string) *Cmd {
	return func(args ...string) *Cmd {
		return env.Cmd(append(
			[]string{"compose", "--workdir", wd, "-f", composePath}, args...)...,
		).WithTmuxTmpdir(tmuxTmpdir)
	}
}

// refusingStopPreMuxYAML is launcherMuxYAML with a stop_pre hook that fails
// under on_error fail, so a down keeps the replica running.
func refusingStopPreMuxYAML(project string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, "300"]
    hooks:
      - name: refuse
        stop_pre:
          args: [sh, -c, "exit 1"]
          on_error: fail
mux:
  layouts:
    - name: solo
      root:
        command: alpha
`, project)
}

// waitForStampedWindows polls until exactly n windows carry identity and
// returns their ids.
func waitForStampedWindows(
	t *testing.T,
	tmuxTmpdir, identity string,
	n int,
	deadline time.Duration,
) []string {
	t.Helper()
	end := time.Now().Add(deadline)
	var ids []string
	for time.Now().Before(end) {
		if ids = windowsStampedTmpdir(t, tmuxTmpdir, identity); len(ids) == n {
			return ids
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("windows stamped %q = %v, want %d; windows on the server: %v",
		identity, ids, n, windowIDsTmpdir(t, tmuxTmpdir))
	return nil
}

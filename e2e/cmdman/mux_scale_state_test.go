package cmdman_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/compose"

	// Link the tmux muxctl driver so the in-process MuxScaleState read below can
	// resolve the "tmux" driver (the binary under test links it from main.go).
	_ "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// muxScaleState reads the project's agreed shown-replica positions in-process,
// through the very method the project-manager backend calls. The selection is
// built the way the CLI invocations below build theirs (-f plus --workdir), so
// the ownership stamp it filters on is the one the dashboards carry.
func muxScaleState(
	ctx context.Context,
	t *testing.T,
	workDir, composePath string,
) map[string]int {
	t.Helper()
	spec, err := compose.LoadAndNormalize(compose.NormalizeOpts{
		File:    composePath,
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("load compose file: %v", err)
	}
	positions, err := compose.NewService(nil).MuxScaleState(ctx, compose.MuxScaleStateOption{
		Selection: compose.SelectionFromSpec(&spec),
	})
	if err != nil {
		t.Fatalf("MuxScaleState: %v", err)
	}
	return positions
}

// tmuxWindowIDInSession returns the @id of the window named windowName in the
// named session. A project with a dashboard in two sessions has the same window
// name twice, so the session is what tells them apart.
func tmuxWindowIDInSession(t *testing.T, socket, session, windowName string) string {
	t.Helper()
	out := tmuxRun(t, socket, "list-windows", "-a", "-F",
		"#{session_name}\t#{window_name}\t#{window_id}")
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 3 && fields[0] == session && fields[1] == windowName {
			return fields[2]
		}
	}
	t.Fatalf("window %q not found in session %q; windows:\n%s", windowName, session, out)
	return ""
}

// TestComposeMuxScaleState_AgreementOnly is D14 against a live tmux: with two
// dashboard windows for one project, a server-wide cycle-scale leaves them
// agreeing and the position is reported, while a session-narrowed one leaves
// them genuinely disagreeing and the command is omitted rather than answered
// with one window's number (the merged read underneath is last-row-wins).
func TestComposeMuxScaleState_AgreementOnly(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "cs-agree"
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

	// Two dashboard windows for one project, produced legitimately with -s.
	for _, session := range []string{"sessA", "sessB"} {
		if _, stderr, err := env.muxExec(
			ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "-s", session,
		); err != nil {
			t.Fatalf("compose mux -s %s failed: %v\nstderr:\n%s", session, err, stderr)
		}
	}
	window := "cmdman-" + project
	widA := tmuxWindowIDInSession(t, socket, "sessA", window)
	widB := tmuxWindowIDInSession(t, socket, "sessB", window)
	waitForPaneTitle(t, socket, widA, "web-1", 3*time.Second)
	waitForPaneTitle(t, socket, widB, "web-1", 3*time.Second)

	// Nothing has been cycled, so no window holds a position: absent everywhere
	// is unknown, not replica 1.
	if got := muxScaleState(ctx, t, wd, composePath); len(got) != 0 {
		t.Fatalf("before any cycle-scale the state should be empty; got %v", got)
	}

	if stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web",
	); err != nil {
		t.Fatalf("cycle-scale web failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	waitForPaneTitle(t, socket, widA, "web-2", 3*time.Second)
	waitForPaneTitle(t, socket, widB, "web-2", 3*time.Second)

	if got := muxScaleState(ctx, t, wd, composePath); got["web"] != 2 {
		t.Fatalf("agreeing windows should report their position; got %v, want web=2", got)
	}

	// -s narrows the enumeration, so sessB is never visited and the two windows
	// end up showing different replicas.
	if stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath,
		"mux", "-s", "sessA", "cycle-scale", "web",
	); err != nil {
		t.Fatalf(
			"cycle-scale -s sessA web failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout, stderr,
		)
	}
	waitForPaneTitle(t, socket, widA, "web-3", 3*time.Second)

	// The disagreement is real, not an artifact of the read: each window holds
	// its own position, and they are not the same one.
	for wid, want := range map[string]string{widA: "web=3", widB: "web=2"} {
		if got := tmuxWindowOption(t, socket, wid, "@cmdman_scale"); got != want {
			t.Fatalf("window %s @cmdman_scale = %q, want %q", wid, got, want)
		}
	}
	if got := muxScaleState(ctx, t, wd, composePath); len(got) != 0 {
		t.Fatalf("web must be omitted while the windows disagree; got %v", got)
	}

	// Cycling server-wide again brings them back into agreement, so the answer
	// comes back rather than staying poisoned.
	if stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "cycle-scale", "web=1",
	); err != nil {
		t.Fatalf("cycle-scale web=1 failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	waitForPaneTitle(t, socket, widA, "web-1", 3*time.Second)
	waitForPaneTitle(t, socket, widB, "web-1", 3*time.Second)
	if got := muxScaleState(ctx, t, wd, composePath); got["web"] != 1 {
		t.Fatalf("re-agreeing windows should report again; got %v, want web=1", got)
	}
}

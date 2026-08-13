package cmdman_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// composeMuxFrameYAML is the smallest project that comes up with a dashboard:
// one service, one layout, on a dedicated tmux socket. The frame is what these
// tests are about, so the dashboard under it stays minimal.
//
// The project name never equals the session name ("cmdman", the outside-tmux
// default) nor the window name it produces ("cmdman-<project>"): a session
// holding a window named like itself cannot be given a second window (the
// driver defect recorded in the frame-verbs plan's STATUS).
func composeMuxFrameYAML(project, socket string) string {
	return fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: solo
      root:
        command: web
`, project, socket)
}

// composeMuxFrameDef is the def the auto-show docks: a two-row bottom bar shown
// through a managed entry (D19/V7). Two rows because a 1-row bar realizes at 0
// usable rows under pane-border-status top (the driver's known off-by-one), and
// managed because the supervised command it creates is what tells a threaded
// RunOptions.Svc from a missing one — the def's Config half alone would not.
const composeMuxFrameDef = `frame:
  - edge: bottom
    size: 2
    command: ["/bin/sh", "-c", "sleep 300"]
    managed: true
`

// composeMuxFrameManaged is the command the def's single managed entry runs as:
// mux.FrameCommandName("dev", 0). Adding an entry above it renames it.
const composeMuxFrameManaged = "frame-dev-0"

// TestComposeMuxFrame is V9 on the compose side: `cmdman compose mux up` docks
// the configured default_frame around the window it brings up, on the same
// terms standalone `mux up` does. The subtests name the two halves the key
// decides — set frames the dashboard, unset leaves it bare — with the def on
// disk either way, so it is the key and not its availability that is under
// test.
//
// The names stay short: each subtest drives a tmux server on a socket derived
// from t.Name(), and the socket path has to fit in sockaddr_un.
func TestComposeMuxFrame(t *testing.T) {
	t.Parallel()
	requireTmux(t)

	// up brings the project's commands up and then its dashboard, returning the
	// socket and the id of the window `compose mux up` built. Creating the
	// commands first is not optional: leaf resolution fails on a project with
	// none, and `compose mux up` would never reach the frame at all.
	up := func(t *testing.T, ctx context.Context, env *testEnv, project string) (string, string) {
		t.Helper()
		wd := composeWorkdir(t)
		socket := muxSocket(t)
		t.Cleanup(func() { killTmuxServer(t, socket) })
		composePath := writeComposeFile(t, wd, composeMuxFrameYAML(project, socket))
		t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

		if _, stderr, err := env.exec(
			ctx, "compose", "--workdir", wd, "-f", composePath, "up",
		); err != nil {
			t.Fatalf("compose up: %v\nstderr:\n%s", err, stderr)
		}
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.workdir="+wd,
			"-l", "cmdman.compose.project="+project,
		) {
			env.waitForState(ctx, e["ID"].(string), "running", defaultTimeout)
		}

		if _, stderr, err := env.muxExec(
			ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "up",
		); err != nil {
			t.Fatalf("compose mux up: %v\nstderr:\n%s", err, stderr)
		}
		return socket, tmuxWindowID(t, socket, "cmdman-"+project)
	}

	t.Run("shown", func(t *testing.T) {
		ctx := frameCleanupContext(t)
		env := newTestEnv(t)
		writeFrameDefFile(t, env, "dev", composeMuxFrameDef)
		// newTestEnv points $CMDMAN_CONF at a file that does not exist; writing
		// it is what puts the key in the resolved configuration, and the frame
		// dir writeFrameDefFile used is this file's sibling.
		must(t, os.WriteFile(env.confPath, []byte(`{"default_frame": "dev"}`), 0o644))
		t.Cleanup(func() { env.cleanupCommand(ctx, composeMuxFrameManaged) })

		socket, wid := up(t, ctx, env, "frmshown")

		if got := tmuxWindowOption(t, socket, wid, "@cmdman_frame_def"); got != "dev" {
			t.Fatalf("@cmdman_frame_def = %q, want the configured dev", got)
		}
		if got := frameStampedPanes(t, socket, wid); len(got) != 1 {
			t.Fatalf("frame panes = %v, want the def's one entry docked", got)
		}
		if got := windowPaneBases(t, socket, wid); !slices.Equal(
			got, []string{"frame-0", "web-1"},
		) {
			t.Fatalf("pane titles = %v, want the frame's bar beside the layout's web-1", got)
		}
		// The managed entry got its supervised command, which is the half of the
		// wiring a Config with no Svc beside it could not have produced.
		env.waitForState(ctx, composeMuxFrameManaged, "running", defaultTimeout)
	})

	t.Run("unset", func(t *testing.T) {
		ctx := frameCleanupContext(t)
		env := newTestEnv(t)
		// The def is on disk and $CMDMAN_CONF names no file: with the key unset
		// the dashboard comes up bare however many defs there are to show.
		writeFrameDefFile(t, env, "dev", composeMuxFrameDef)

		socket, wid := up(t, ctx, env, "frmbare")

		if got := tmuxWindowOption(t, socket, wid, "@cmdman_frame_def"); got != "" {
			t.Fatalf("@cmdman_frame_def = %q, want it unset", got)
		}
		if got := frameStampedPanes(t, socket, wid); len(got) != 0 {
			t.Fatalf("frame panes = %v, want the window bare", got)
		}
		if got := windowPaneBases(t, socket, wid); !slices.Equal(got, []string{"web-1"}) {
			t.Fatalf("pane titles = %v, want the layout's web-1 alone", got)
		}
	})
}

// frameCleanupContext is the context the frame subtests run and clean up under.
// context.Background, not t.Context: the cleanups stop supervised commands
// through the binary, and t.Context is already canceled by the time one runs.
func frameCleanupContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// frameStampedPanes returns the ids of windowID's frame-stamped panes — the
// panes a shown frame owns, as opposed to the dashboard's own.
func frameStampedPanes(t *testing.T, socket, windowID string) []string {
	t.Helper()
	var ids []string
	for _, line := range tmuxPaneField(
		t, socket, windowID, "#{pane_id}\t#{@cmdman_frame}",
	) {
		if id, stamp, ok := strings.Cut(line, "\t"); ok && stamp != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

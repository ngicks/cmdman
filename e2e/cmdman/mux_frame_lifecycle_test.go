package cmdman_test

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman"
)

// The frame lifecycle end to end: one window carries a project through every
// verb the fixture is supposed to outlive. See TestFrameLifecycle.

const (
	// frameLifeSession is the tmux session the lifecycle runs in. Its one window
	// is named after it, which is the name `mux up -s <session>` builds in.
	frameLifeSession = "life"
	// frameLifeProject is the compose project the switcher switches to.
	frameLifeProject = "swp"
	// frameLifeManaged is the supervised command the def's managed entry is shown
	// through: mux.FrameCommandName("dev", 1), the second entry of def "dev".
	// Reordering frameLifeDef's entries renames it.
	frameLifeManaged = "frame-dev-1"
)

// frameLifeDef docks the switcher the switch leg drives on the left, and a
// managed entry (D19/V7) along the bottom — the supervised command that must
// outlive every leg. The bottom bar is 3 rows because a 1-row bar realizes at 0
// usable rows under pane-border-status top (the driver's known off-by-one).
const frameLifeDef = `frame:
  - edge: left
    size: 40
    component: switcher
  - edge: bottom
    size: 3
    command: ["/bin/sh", "-c", "sleep 300"]
    managed: true
`

// frameLifeMuxYAML is the dashboard the project arrives as: two layouts of
// different shape, so cycling to the next one is visible in the pane titles. No
// driver_opt.socket — the frame verbs take no --socket, so the whole test runs
// on the default socket under a private TMUX_TMPDIR.
const frameLifeMuxYAML = `mux:
  layouts:
    - name: pair
      root:
        dir: h
        splits: [1, 1]
        panes: [web, worker]
    - name: solo
      root:
        command: web
`

// frameLifeComposeYAML is the project the docked switcher switches to: a compose
// project, because the switcher lists projects and drops standalone commands.
func frameLifeComposeYAML(project string) string {
	return fmt.Sprintf(`name: %s
commands:
  one:
    args: [sleep, "300"]
mux:
  layouts:
    - name: solo
      root:
        command: one
`, project)
}

// TestFrameLifecycle is the parent plan's step-15 criterion, quoted: "chrome
// survives project switch/stop/relaunch". A dashboard is built, framed, and then
// carried through a layout cycle, a switch away driven through the docked
// switcher, `mux down` and a relaunch. The frame must be the same panes
// throughout — the same tmux ids, never torn down and rebuilt — and the managed
// entry the same supervised command, until the explicit hide at the end takes
// the chrome down and leaves that command running (D19/V7).
//
// The frame goes up around a project rather than ahead of one because the verbs
// are pointed at the window with -s: an up that names a session builds the
// project's own window, and only the window already stamped for that project is
// reused. Show-before-launch — chrome put up first, the project landing in the
// region it leaves over — is the current-window takeover, which needs a caller
// sitting in the window; the muxctl unit tests cover it. For the same reason the
// relaunch after `mux down` arrives beside the chrome rather than inside it:
// down handed the window back, leaving nothing to recognize it by.
//
// The switch is as much of one as a server with no client can be: select-window
// moves the project's session onto its window, and the switch-client half has no
// client to move.
//
// Not parallel: the switch leg reads a live widget rendering in a pane, which is
// the one leg whose timing matters.
func TestFrameLifecycle(t *testing.T) {
	requireTmux(t)
	// context.Background, not t.Context: the cleanups below stop supervised
	// commands through the binary, and t.Context is already canceled by the time
	// a cleanup runs. t.Cleanup(cancel) rather than defer for the same reason.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	writeFrameDefFile(t, env, "dev", frameLifeDef)

	for _, name := range []string{"web", "worker"} {
		env.run(ctx, "run", "-n", name, "--", "/bin/sh", "-c", "sleep 300")
		t.Cleanup(func() { env.cleanupCommand(ctx, name) })
		env.waitForState(ctx, name, "running", defaultTimeout)
	}
	specPath := writeSpecFile(t, frameLifeMuxYAML)

	// The switch target's commands are brought up first, so the switcher lists
	// the project from the store the moment it starts; its window arrives later,
	// at the switch leg itself.
	wd := composeWorkdir(t)
	composePath := writeComposeFile(t, wd, frameLifeComposeYAML(frameLifeProject))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, frameLifeProject) })
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "up",
	); err != nil {
		t.Fatalf("compose up: %v\nstderr:\n%s", err, stderr)
	}

	shellWid := startFrameLifeSession(t, env, tmuxTmpdir, wd)

	muxUp := func(leg string) {
		t.Helper()
		if _, stderr, err := env.muxExecWithTmpdir(
			ctx, tmuxTmpdir, "mux", "up", specPath, "-s", frameLifeSession,
		); err != nil {
			t.Fatalf("mux up (%s): %v\nstderr:\n%s", leg, err, stderr)
		}
	}

	// ---- the dashboard is built, and the chrome goes up around it ----

	muxUp("first")
	wid := windowIDStamped(t, tmuxTmpdir, frameLifeSession)
	// The shell window has served its purpose. Killing it leaves the dashboard as
	// the session's only — and so its current — window, which is what the frame
	// verbs target and what the one-window assertions below read.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "kill-window", "-t", shellWid)
	if got := tmuxFormat(t, tmuxTmpdir, wid, "#{window_width}x#{window_height}"); got != "200x50" {
		t.Fatalf("window is %s, want the 200x50 the session was created at: a clamped "+
			"window truncates the frame the assertions below read", got)
	}
	if want := []string{"web", "worker"}; !slices.Equal(
		readPanes(t, tmuxTmpdir, wid).titles, want,
	) {
		t.Fatalf("project panes after mux up = %v, want %v",
			readPanes(t, tmuxTmpdir, wid).titles, want)
	}
	if got := sessionWindowIDs(t, tmuxTmpdir, frameLifeSession); len(got) != 1 {
		t.Fatalf("session %s holds windows %v, want only the dashboard %s",
			frameLifeSession, got, wid)
	}

	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "mux", "frame", "show", "dev", "-s", frameLifeSession,
	); err != nil {
		t.Fatalf("mux frame show: %v\nstderr:\n%s", err, stderr)
	}
	if got := tmuxFormat(t, tmuxTmpdir, wid, "#{@cmdman_frame_def}"); got != "dev" {
		t.Fatalf("@cmdman_frame_def = %q after show, want dev", got)
	}
	shown := readPanes(t, tmuxTmpdir, wid)
	if len(shown.frame) != 2 {
		t.Fatalf("frame panes after show = %v, want the def's two entries", shown.frame)
	}
	framePanes := shown.frame
	// The other read-back path (V4) must agree with the window option.
	if got := frameLsWindows(t, ctx, env, tmuxTmpdir, "dev"); got != wid {
		t.Fatalf("mux frame ls reports dev on %q, want the framed window %s", got, wid)
	}

	env.waitForState(ctx, frameLifeManaged, "running", defaultTimeout)
	t.Cleanup(func() { env.cleanupCommand(ctx, frameLifeManaged) })
	managedID, _ := env.inspectJSON(ctx, frameLifeManaged)["ID"].(string)

	// intact is what every leg but the last asserts: the same frame panes by id
	// (a count would pass for a frame torn down and rebuilt, which would have
	// killed what its panes ran), the same def, and the managed entry still the
	// command show created.
	intact := func(leg string) lifePanes {
		t.Helper()
		p := readPanes(t, tmuxTmpdir, wid)
		if !slices.Equal(p.frame, framePanes) {
			t.Fatalf("frame panes after %s = %v, want the untouched %v", leg, p.frame, framePanes)
		}
		if got := tmuxFormat(t, tmuxTmpdir, wid, "#{@cmdman_frame_def}"); got != "dev" {
			t.Fatalf("@cmdman_frame_def = %q after %s, want dev", got, leg)
		}
		got := env.inspectJSON(ctx, frameLifeManaged)
		if got["ID"] != managedID || got["State"] != "running" {
			t.Fatalf("the managed command is %v/%v after %s, want %s running",
				got["ID"], got["State"], leg, managedID)
		}
		return p
	}

	// The chrome went up around a project that was already there, and left it
	// running: the frame resizes the main region, it never rebuilds it.
	p := intact("mux frame show")
	if want := []string{"web", "worker"}; !slices.Equal(p.titles, want) {
		t.Fatalf("project panes after mux frame show = %v, want the untouched %v",
			p.titles, want)
	}

	// ---- a layout cycle rebuilds the project region, not the frame ----

	muxUp("layout cycle")
	p = intact("the layout cycle")
	if want := []string{"web"}; !slices.Equal(p.titles, want) {
		t.Fatalf("project panes after the layout cycle = %v, want %v", p.titles, want)
	}

	// ---- the switch, driven through the docked switcher (D6/V6) ----

	// The switch target's dashboard goes up in its own session — the default
	// "cmdman", since the invocation names no session and runs outside tmux — so
	// the switch has somewhere else to go.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"compose", "--workdir", wd, "-f", composePath, "mux", "up",
	); err != nil {
		t.Fatalf("compose mux up: %v\nstderr:\n%s", err, stderr)
	}
	projWid := windowIDNamed(t, tmuxTmpdir, "cmdman", "cmdman-"+frameLifeProject)
	// The driver creates its windows detached, so the switch is the only thing
	// that can make the dashboard its session's current window — without this the
	// assertion below would be satisfied by an event that never happened.
	if got := currentWindowID(t, tmuxTmpdir, "cmdman"); got == projWid {
		t.Fatalf("session cmdman is already on the dashboard %s before the switch", got)
	}

	switcher := paneWithTitle(t, tmuxTmpdir, wid, "frame-0")
	// waitFor polls read until want accepts it, failing with the last value and
	// what the switcher is showing: a bare timeout here says nothing about which
	// half of the switch did not happen.
	waitFor := func(what string, want func(string) bool, read func() string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			got := read()
			if want(got) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s; last read %q, switcher pane:\n%s",
					what, got, capturePane(t, tmuxTmpdir, switcher))
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	// The head names the project's directory, not the project (D44), so the
	// listing is observed through the directory the project was brought up in.
	waitFor(
		"the docked switcher never listed the project",
		func(s string) bool { return strings.Contains(s, filepath.Base(wd)) },
		func() string { return capturePane(t, tmuxTmpdir, switcher) },
	)
	// One project is listed, so the initial selection is it and enter is the
	// whole gesture.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "send-keys", "-t", switcher, "Enter")
	// select-window is what the switch amounts to on a server with no client
	// attached (muxctl/tmux.clientToSwitch moves nobody when there is nobody), so
	// the dashboard becoming its session's current window is the observable.
	waitFor(
		"enter in the switcher never made the project's window current",
		func(s string) bool { return s == projWid },
		func() string { return currentWindowID(t, tmuxTmpdir, "cmdman") },
	)
	intact("the switch")

	// ---- the project is stopped; teardown is per-side ----

	stdout, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "mux", "down", "-s", frameLifeSession,
	)
	if err != nil {
		t.Fatalf("mux down: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Restored window") {
		t.Fatalf("mux down printed %q, want a restored-window line", stdout)
	}
	p = intact("mux down")
	if len(p.project) != 1 {
		t.Fatalf("project panes after mux down = %v, want the one default pane", p.project)
	}
	if got := tmuxFormat(t, tmuxTmpdir, wid, "#{@cmdman_window}"); got != "" {
		t.Fatalf("@cmdman_window = %q after mux down, want it cleared", got)
	}

	// ---- the relaunch builds a new dashboard; the chrome outlives it ----

	// `down` handed this window back to the user, stamp and all, so the relaunch
	// has nothing to recognize it by and builds its dashboard beside it. What the
	// criterion asks of the chrome is unchanged: it is still standing, still the
	// same panes, still running the same managed command.
	muxUp("relaunch")
	relaunched := windowIDStamped(t, tmuxTmpdir, frameLifeSession)
	if relaunched == wid {
		t.Fatalf("the relaunch landed back in the restored window %s", wid)
	}
	p = intact("the relaunch")
	if got := windowPaneCount(t, tmuxTmpdir, wid); got != len(p.frame)+1 {
		t.Fatalf("the framed window holds %d panes after the relaunch, want its %d "+
			"frame panes plus the one default pane down left", got, len(p.frame))
	}
	if want := []string{"web", "worker"}; !slices.Equal(
		readPanes(t, tmuxTmpdir, relaunched).titles, want,
	) {
		t.Fatalf("project panes on the relaunched dashboard = %v, want %v",
			readPanes(t, tmuxTmpdir, relaunched).titles, want)
	}

	// ---- only the explicit hide takes the chrome down ----

	// hide acts on the target session's current window, which is still the framed
	// one: the switch moved the dashboard's own session, and the relaunch built
	// its window detached.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "mux", "frame", "hide", "-s", frameLifeSession,
	); err != nil {
		t.Fatalf("mux frame hide: %v\nstderr:\n%s", err, stderr)
	}
	p = readPanes(t, tmuxTmpdir, wid)
	if len(p.frame) != 0 {
		t.Fatalf("frame panes after hide = %v, want none", p.frame)
	}
	if got := tmuxFormat(t, tmuxTmpdir, wid, "#{@cmdman_frame_def}"); got != "" {
		t.Fatalf("@cmdman_frame_def = %q after hide, want it cleared", got)
	}
	if got := windowPaneCount(t, tmuxTmpdir, wid); got != 1 {
		t.Fatalf("the window holds %d panes after hide, want the single pane a "+
			"fully restored window is left as", got)
	}
	if got := frameLsWindows(t, ctx, env, tmuxTmpdir, "dev"); got != "-" {
		t.Fatalf("mux frame ls still reports dev on %q after hide", got)
	}
	// D19/V7's whole point: the frame is gone, its managed command is not.
	got := env.inspectJSON(ctx, frameLifeManaged)
	if got["ID"] != managedID || got["State"] != "running" {
		t.Fatalf("the managed command is %v/%v after hide, want %s still running",
			got["ID"], got["State"], managedID)
	}
}

// startFrameLifeSession starts the test's own tmux server with its one session
// and returns the id of that session's shell window.
//
// The server is started with the cmdman environment, and it is also set on the
// server explicitly: a docked widget's argv carries no --data-dir
// (frame.WidgetArgv), so the switcher in a frame pane finds the test's runtime
// only through the environment tmux hands its panes.
func startFrameLifeSession(t *testing.T, e *testEnv, tmuxTmpdir, startDir string) string {
	t.Helper()

	cmdmanEnv := [][2]string{
		{cmdman.ENV_CMDMAN_DATA_DIR, e.dataHome},
		{cmdman.ENV_CMDMAN_RUNTIME_DIR, e.runtimeDir},
		{cmdman.ENV_CMDMAN_CONF, e.confPath},
	}
	cmd := exec.Command("tmux",
		"new-session", "-d",
		"-s", frameLifeSession, "-n", frameLifeSession,
		"-x", "200", "-y", "50", "-c", startDir,
	)
	cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
	for _, kv := range cmdmanEnv {
		cmd.Env = append(cmd.Env, kv[0]+"="+kv[1])
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	for _, kv := range cmdmanEnv {
		tmuxRunWithTmpdir(t, tmuxTmpdir, "set-environment", "-g", kv[0], kv[1])
	}

	return currentWindowID(t, tmuxTmpdir, frameLifeSession)
}

// windowIDStamped returns the id of the one window on the server whose
// @cmdman_window ownership option equals identity — how the test addresses a
// window `mux up` built for itself, and the same stamp the up goes by.
func windowIDStamped(t *testing.T, tmuxTmpdir, identity string) string {
	t.Helper()
	out := tmuxRunWithTmpdir(t, tmuxTmpdir, "list-windows", "-a",
		"-F", "#{window_id}\t#{@cmdman_window}")
	var found []string
	for line := range strings.SplitSeq(out, "\n") {
		if id, stamp, ok := strings.Cut(line, "\t"); ok && stamp == identity {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		t.Fatalf("windows stamped %q = %v, want exactly one; windows:\n%s",
			identity, found, out)
	}
	return found[0]
}

// lifePanes is one window's panes split by the frame stamp: which are the
// frame's, which are the project's, and the project ones' border titles.
type lifePanes struct {
	frame   []string
	project []string
	titles  []string // sorted, project panes only
}

// readPanes reads windowID's panes in tmux list order. The pane id leads the
// format although the titles are read positionally: the runner trims the whole
// output, so a format starting with the (empty) frame stamp would lose the first
// pane's leading tab.
func readPanes(t *testing.T, tmuxTmpdir, windowID string) lifePanes {
	t.Helper()
	out := tmuxRunWithTmpdir(t, tmuxTmpdir, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{@cmdman_frame}\t#{pane_title}")
	var p lifePanes
	for line := range strings.SplitSeq(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		if parts[1] != "" {
			p.frame = append(p.frame, parts[0])
			continue
		}
		p.project = append(p.project, parts[0])
		if len(parts) == 3 && parts[2] != "" {
			p.titles = append(p.titles, parts[2])
		}
	}
	slices.Sort(p.titles)
	return p
}

// windowPaneCount counts the window's panes. It reads them itself rather than
// through [readPanes]: a lone untitled pane — what a restored window is left as
// — carries nothing after its id, and the runner's trailing trim takes the empty
// fields with it, so readPanes cannot see it.
func windowPaneCount(t *testing.T, tmuxTmpdir, windowID string) int {
	t.Helper()
	out := tmuxRunWithTmpdir(t, tmuxTmpdir, "list-panes", "-t", windowID, "-F", "#{pane_id}")
	if out == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

// paneWithTitle returns the id of the pane carrying the given border title —
// the frame's entry panes are titled frame.EntryPaneName(i).
func paneWithTitle(t *testing.T, tmuxTmpdir, windowID, title string) string {
	t.Helper()
	out := tmuxRunWithTmpdir(t, tmuxTmpdir, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{pane_title}")
	for line := range strings.SplitSeq(out, "\n") {
		if id, got, ok := strings.Cut(line, "\t"); ok && got == title {
			return id
		}
	}
	t.Fatalf("no pane titled %q in window %s; panes:\n%s", title, windowID, out)
	return ""
}

// capturePane returns what a pane is currently displaying.
func capturePane(t *testing.T, tmuxTmpdir, paneID string) string {
	t.Helper()
	return tmuxRunWithTmpdir(t, tmuxTmpdir, "capture-pane", "-p", "-t", paneID)
}

// tmuxFormat expands a tmux format against target, which yields "" for an
// option that was never set instead of failing.
func tmuxFormat(t *testing.T, tmuxTmpdir, target, format string) string {
	t.Helper()
	return tmuxRunWithTmpdir(t, tmuxTmpdir, "display-message", "-p", "-t", target, format)
}

// currentWindowID returns the session's current window — what the switcher's
// selection moves, and what a client attached to that session would be looking
// at.
func currentWindowID(t *testing.T, tmuxTmpdir, session string) string {
	t.Helper()
	return tmuxFormat(t, tmuxTmpdir, "="+session+":", "#{window_id}")
}

// windowIDNamed returns the id of the session's window of that name.
func windowIDNamed(t *testing.T, tmuxTmpdir, session, name string) string {
	t.Helper()
	out := tmuxRunWithTmpdir(t, tmuxTmpdir, "list-windows", "-t", "="+session,
		"-F", "#{window_name}\t#{window_id}")
	for line := range strings.SplitSeq(out, "\n") {
		if got, id, ok := strings.Cut(line, "\t"); ok && got == name {
			return id
		}
	}
	t.Fatalf("no window named %q in session %s; windows:\n%s", name, session, out)
	return ""
}

// sessionWindowIDs returns every window id in the session.
func sessionWindowIDs(t *testing.T, tmuxTmpdir, session string) []string {
	t.Helper()
	out := tmuxRunWithTmpdir(t, tmuxTmpdir, "list-windows", "-t", "="+session,
		"-F", "#{window_id}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// frameLsWindows returns the WINDOWS column of `mux frame ls`'s row for def
// ("-" when it is shown nowhere).
func frameLsWindows(
	t *testing.T,
	ctx context.Context,
	e *testEnv,
	tmuxTmpdir, def string,
) string {
	t.Helper()
	stdout, stderr, err := e.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "mux", "frame", "ls", "-s", frameLifeSession,
	)
	if err != nil {
		t.Fatalf("mux frame ls: %v\nstderr:\n%s", err, stderr)
	}
	for line := range strings.SplitSeq(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == def {
			return fields[1]
		}
	}
	t.Fatalf("no row for def %q in mux frame ls:\n%s", def, stdout)
	return ""
}

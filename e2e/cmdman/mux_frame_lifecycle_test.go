package cmdman_test

import (
	"context"
	"fmt"
	"os/exec"
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
// survives project switch/stop/relaunch". One framed window carries a project
// through `mux up`, a layout cycle, a switch away driven through the docked
// switcher, `mux down` and a relaunch. The frame must be the same panes
// throughout — the same tmux ids, never torn down and rebuilt — and the managed
// entry the same supervised command, until the explicit hide at the end takes
// the chrome down and leaves that command running (D19/V7).
//
// The verbs are pointed at the window with -s, the documented targeting path for
// a caller outside tmux and the one cmdman/mux's own tests use. So the driver's
// "a window holding a frame but no project is taken over in place" branch is not
// what puts the project under the frame here; that stays covered by the muxctl
// unit tests. The switch is likewise as much of one as a server with no client
// can be: select-window moves the project's session onto its window, and the
// switch-client half has no client to move.
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

	wid := startFrameLifeSession(t, env, tmuxTmpdir, wd)
	if got := tmuxFormat(t, tmuxTmpdir, wid, "#{window_width}x#{window_height}"); got != "200x50" {
		t.Fatalf("window is %s, want the 200x50 it was created at: a clamped window "+
			"truncates the frame the assertions below read", got)
	}

	// ---- the frame goes up on a window holding no project (D15, driver F6) ----

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

	muxUp := func(leg string) {
		t.Helper()
		if _, stderr, err := env.muxExecWithTmpdir(
			ctx, tmuxTmpdir, "mux", "up", specPath, "-s", frameLifeSession,
		); err != nil {
			t.Fatalf("mux up (%s): %v\nstderr:\n%s", leg, err, stderr)
		}
	}

	// ---- the project lands under the standing frame ----

	muxUp("first")
	p := intact("mux up")
	if got := tmuxFormat(t, tmuxTmpdir, wid, "#{@cmdman_window}"); got != frameLifeSession {
		t.Fatalf("@cmdman_window = %q after mux up, want %q: the dashboard landed "+
			"beside the frame rather than under it", got, frameLifeSession)
	}
	if want := []string{"web", "worker"}; !slices.Equal(p.titles, want) {
		t.Fatalf("project panes after mux up = %v, want %v", p.titles, want)
	}
	if got := sessionWindowIDs(t, tmuxTmpdir, frameLifeSession); len(got) != 1 {
		t.Fatalf("session %s holds windows %v, want only the framed %s",
			frameLifeSession, got, wid)
	}

	// ---- a layout cycle rebuilds the project region, not the frame ----

	muxUp("layout cycle")
	p = intact("the layout cycle")
	if want := []string{"web"}; !slices.Equal(p.titles, want) {
		t.Fatalf("project panes after the layout cycle = %v, want %v", p.titles, want)
	}

	// ---- the switch, driven through the docked switcher (D6/V6) ----

	// The project's dashboard goes up in its own session — the default one, since
	// the invocation is outside tmux — rather than beside the framed window: a
	// second window in session "life" cannot be created while a window there is
	// named like the session (see the driver note in this plan's STATUS).
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
	waitFor(
		"the docked switcher never listed the project",
		func(s string) bool { return strings.Contains(s, frameLifeProject) },
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

	// ---- and the relaunch arrives inside the chrome that was waiting ----

	muxUp("relaunch")
	p = intact("the relaunch")
	if want := []string{"web", "worker"}; !slices.Equal(p.titles, want) {
		t.Fatalf("project panes after the relaunch = %v, want %v", p.titles, want)
	}

	// ---- only the explicit hide takes the chrome down ----

	// hide acts on the target session's current window, which is still the framed
	// one: the switch moved the dashboard's own session, not this one.
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
	if want := []string{"web", "worker"}; !slices.Equal(p.titles, want) {
		t.Fatalf("project panes after hide = %v, want the untouched %v", p.titles, want)
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
// and returns the id of the window the frame verbs target.
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

	wid := currentWindowID(t, tmuxTmpdir, frameLifeSession)
	// `mux up` finds its window by name, so a window tmux renamed out from under
	// it would be answered with a second one — which reads exactly like the frame
	// having vanished.
	tmuxRunWithTmpdir(t, tmuxTmpdir, "set-option", "-w", "-t", wid, "automatic-rename", "off")
	return wid
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

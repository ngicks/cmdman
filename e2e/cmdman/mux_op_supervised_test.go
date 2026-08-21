package cmdman_test

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
)

// A mux verb that rebuilds a window can close the very pane it was typed in.
// These tests type the verbs into real tmux panes and then watch the server,
// because from the invoking pane's point of view the run is over the moment
// that pane dies — the only place the rest of the run shows up is the window it
// left behind.
//
// Two ways it used to end badly are what the assertions are aimed at:
//
//   - a shell window taken over by the dashboard: the layout appeared, but the
//     frame the configuration asks for never did, because the shell running the
//     command was consumed just before the frame was put up;
//   - a command typed inside a dashboard: the panes were killed and nothing was
//     built in their place, leaving their corpses on screen and the window still
//     holding the "keep dead panes" setting the rebuild turns on.
//
// So every test here asks for the run's later phases, not just its first, and
// for the window to be left with nothing switched on that only belongs to a run
// in progress.
//
// The pane-driving machinery they share is in mux_op_pane_driver_test.go.

// TestMuxOp_UpTakesOverTheWindowItWasTypedIn is the shell-window takeover: the
// dashboard is asked for from a single-pane shell window named like the window
// it will become, so the pane holding the command is the pane the layout claims
// first.
//
// The frame is the assertion that matters. Putting it up is the last thing an
// up does, and it is what used to be missing: the layout arrived and the window
// stayed frameless, because the shell was consumed a moment too early.
func TestMuxOp_UpTakesOverTheWindowItWasTypedIn(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := frameCleanupContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	writeDefaultFrameConfig(t, env, "dev")
	startMuxOpCommands(t, ctx, env, "api", "worker")
	specPath := writeSpecFile(t, muxOpPairYAML(socket))

	// The session and its window are both named "cmdman", which is the name the
	// dashboard takes outside any other instruction, so the window the command
	// is typed in is the window the up wants.
	shellWid, shellPane := startMuxOpSession(t, env, socket, "cmdman", "cmdman")

	c := sendMuxOpCommand(t, socketSender(t, socket), shellPane,
		cmdmanBin+" mux up "+specPath)

	waitForMuxOp(t, "the dashboard never came up inside its frame",
		func() string { return tmuxWindowOption(t, socket, shellWid, "@cmdman_frame_def") },
		"dev", c)
	waitForWindowSettled(t, socket, shellWid, c)

	if got := tmuxWindowIDByIdentity(t, socket, "cmdman"); got != shellWid {
		t.Fatalf("the dashboard is on window %s, want the shell window %s it was typed in",
			got, shellWid)
	}
	if got, want := muxOpPaneTitles(t, socket, shellWid), []string{
		"api", "frame-0", "worker",
	}; !slices.Equal(got, want) {
		t.Fatalf("pane titles = %v, want the layout's two panes beside the frame's bar %v",
			got, want)
	}
	if got := frameStampedPanes(t, socket, shellWid); len(got) != 1 {
		t.Fatalf("frame panes = %v, want the def's one entry docked", got)
	}
	assertLayoutStamp(t, socket, shellWid, "0")

	// The viewers are the point of the panes, so they have to be running rather
	// than merely present.
	for _, s := range tmuxPaneField(t, socket, shellWid, "#{pane_start_command}") {
		if s != "/bin/sh" && !strings.Contains(s, "attach") {
			t.Errorf("pane runs %q, want a viewer or the frame's shell", s)
		}
	}
}

// TestMuxOp_UpFromInsideTheDashboard is the second way it used to end badly:
// the command is typed in a pane the user opened inside a dashboard, so the
// rebuild kills that pane along with the viewers it is replacing.
//
// What is asserted is that something was built in their place: the window holds
// the layout's panes, all of them alive, none of them a corpse tmux is keeping
// on screen, and the window is no longer set to keep corpses at all.
func TestMuxOp_UpFromInsideTheDashboard(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := frameCleanupContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	startMuxOpCommands(t, ctx, env, "api", "worker")
	specPath := writeSpecFile(t, muxOpPairYAML(socket))
	startMuxOpSession(t, env, socket, "work", "shell")

	if _, stderr, err := env.muxExec(ctx, "mux", "up", specPath, "-s", "work"); err != nil {
		t.Fatalf("mux up: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowIDByIdentity(t, socket, "work")
	if got := len(projectPanes(readOpPanes(t, socket, wid))); got != 2 {
		t.Fatalf("the dashboard holds %d panes before the rebuild, want the layout's 2", got)
	}

	// A shell opened inside the dashboard, the way a user splits one off to run
	// something beside their viewers. It belongs to no layout, so the rebuild
	// treats it as one more pane to clear.
	extra := tmuxRun(t, socket, "split-window", "-d", "-t", wid, "-P", "-F", "#{pane_id}")
	if got := len(projectPanes(readOpPanes(t, socket, wid))); got != 3 {
		t.Fatalf("the dashboard holds %d panes after splitting one off, want 3", got)
	}

	c := sendMuxOpCommand(t, socketSender(t, socket), extra,
		cmdmanBin+" mux up "+specPath)

	// The titles are what says the layout was put back rather than merely torn
	// down: a pane only wears one once the rebuild has finished with it.
	waitForMuxOp(t, "the dashboard was never rebuilt after the pane it was typed in died",
		func() string { return strings.Join(muxOpPaneTitles(t, socket, wid), ",") },
		"api,worker", c)
	waitForWindowSettled(t, socket, wid, c)

	for _, p := range readOpPanes(t, socket, wid) {
		if p.id == extra {
			t.Errorf("the pane the command was typed in (%s) is still there; "+
				"the rebuild did not reach it", extra)
		}
	}
	assertLayoutStamp(t, socket, wid, "0")
}

// TestMuxOp_UpWithNoPaneToRunIn drives the verb the way a key binding does:
// through tmux itself, with no pane behind the invocation at all. Nothing is
// destroyed here and nothing is watching either, so the whole run — layout and
// frame both — has to happen without anybody following it.
func TestMuxOp_UpWithNoPaneToRunIn(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := frameCleanupContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	writeDefaultFrameConfig(t, env, "dev")
	startMuxOpCommands(t, ctx, env, "api", "worker")
	specPath := writeSpecFile(t, muxOpPairYAML(socket))
	shellWid, _ := startMuxOpSession(t, env, socket, "cmdman", "cmdman")

	dir := t.TempDir()
	c := muxOpCapture{
		stdoutPath: filepath.Join(dir, "output"),
		statusPath: filepath.Join(dir, "status"),
	}
	tmuxRun(t, socket, "run-shell", "-b", fmt.Sprintf(
		"%s mux up %s > %s 2>&1; echo $? > %s",
		cmdmanBin, specPath, c.stdoutPath, c.statusPath,
	))

	waitForMuxOp(t, "the dashboard never came up inside its frame",
		func() string { return tmuxWindowOption(t, socket, shellWid, "@cmdman_frame_def") },
		"dev", c)

	waitForWindowSettled(t, socket, shellWid, c)

	if got := c.exitStatus(t); got != 0 {
		t.Fatalf("exit status %d, want 0; output:\n%s", got, c.output())
	}
	if got, want := muxOpPaneTitles(t, socket, shellWid), []string{
		"api", "frame-0", "worker",
	}; !slices.Equal(got, want) {
		t.Fatalf("pane titles = %v, want %v", got, want)
	}
	assertLayoutStamp(t, socket, shellWid, "0")
}

// muxOpScaledComposeYAML is a project with a replicated service shown through
// an unpinned leaf, so the replica on screen can be advanced.
func muxOpScaledComposeYAML(project, socket string) string {
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

// TestMuxOp_ReachesWindowsPastTheFirst is what a run dying with its own pane
// used to cost: a project with a dashboard in two sessions is two windows to
// visit, and an operation typed inside the first one used to get no further
// than that one.
//
// Both halves are driven from a pane inside the first window. Advancing the
// replica leaves that pane alone, so its exit status is readable and both
// windows must show the new replica; tearing the dashboards down closes it
// along with the rest of the region, and the second window must be restored all
// the same.
func TestMuxOp_ReachesWindowsPastTheFirst(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := frameCleanupContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	// The frame is here for its shell: it is a pane inside the dashboard that
	// belongs to no layout, which is what lets a command be typed inside the
	// window without the window reading as half-rebuilt.
	writeDefaultFrameConfig(t, env, "dev")

	wd := composeWorkdir(t)
	project := "twowin"
	composePath := writeComposeFile(t, wd, muxOpScaledComposeYAML(project, socket))
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

	// One dashboard per session. Naming the session is what keeps the second up
	// from finding the first one's window and reusing it, so the project ends up
	// holding two windows that answer to the same ownership stamp.
	for _, session := range []string{"one", "two"} {
		startMuxOpSession(t, env, socket, session, "sh-"+session)
		if _, stderr, err := env.muxExec(
			ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "up", "-s", session,
		); err != nil {
			t.Fatalf("compose mux up -s %s: %v\nstderr:\n%s", session, err, stderr)
		}
	}

	identity := compose.ProjectSelection{WorkDir: wd, Project: project}.ProjectIdentity()
	stamped := muxOpStampedWindows(t, socket, identity)
	if len(stamped) != 2 {
		t.Fatalf("the project holds windows %v, want one per session", stamped)
	}
	first, second := stamped[0], stamped[1]
	for _, wid := range stamped {
		waitForPaneTitle(t, socket, wid, "web-1", muxOpDeadline)
	}

	composeMux := fmt.Sprintf("%s compose --workdir %s -f %s mux", cmdmanBin, wd, composePath)

	// ---- advancing the replica, typed inside the first window ----

	framePane := muxOpPaneTitled(t, socket, first, "frame-0")
	cycle := sendMuxOpCommand(
		t, socketSender(t, socket), framePane, composeMux+" cycle-scale web",
	)

	for _, wid := range stamped {
		waitForMuxOp(t, "window "+wid+" never advanced to the next replica",
			func() string {
				return strconv.FormatBool(
					slices.Contains(muxOpPaneTitles(t, socket, wid), "web-2"),
				)
			}, "true", cycle)
		waitForWindowSettled(t, socket, wid, cycle)
	}
	if got := cycle.exitStatus(t); got != 0 {
		t.Fatalf("cycle-scale exit status %d, want 0; output:\n%s", got, cycle.output())
	}
	for _, session := range []string{"one", "two"} {
		if !strings.Contains(cycle.output(), session+":") {
			t.Errorf("cycle-scale output does not report session %q:\n%s",
				session, cycle.output())
		}
	}
	for _, wid := range stamped {
		assertLayoutStamp(t, socket, wid, "0")
	}

	// ---- tearing both down, typed inside the first window ----

	extra := tmuxRun(t, socket, "split-window", "-d", "-t", first, "-P", "-F", "#{pane_id}")
	down := sendMuxOpCommand(t, socketSender(t, socket), extra, composeMux+" down")

	waitForMuxOp(t, "the dashboards were never given back",
		func() string { return strconv.Itoa(len(muxOpStampedWindows(t, socket, identity))) },
		"0", down)

	for _, wid := range []string{first, second} {
		waitForWindowSettled(t, socket, wid, down)
		if got := len(projectPanes(readOpPanes(t, socket, wid))); got != 1 {
			t.Errorf("window %s holds %d panes outside its frame, want the one pane a "+
				"restored region is left as", wid, got)
		}
		assertLayoutStamp(t, socket, wid, "")
	}
	for _, p := range readOpPanes(t, socket, first) {
		if p.id == extra {
			t.Errorf("the pane the teardown was typed in (%s) is still there", extra)
		}
	}
}

// muxOpStampedWindows returns the ids of the windows carrying identity, in the
// order the server lists them.
func muxOpStampedWindows(t *testing.T, socket, identity string) []string {
	t.Helper()
	out := tmuxRun(t, socket, "list-windows", "-a", "-F", "#{window_id}\t#{@cmdman_window}")
	var found []string
	for line := range strings.SplitSeq(out, "\n") {
		if id, stamp, ok := strings.Cut(line, "\t"); ok && stamp == identity {
			found = append(found, id)
		}
	}
	return found
}

// TestMuxOp_FrameCycleFromAFramePane replaces the frame from inside the frame
// itself: the command is typed in one of the panes the replacement takes down.
//
// Both halves of the replacement have to land. Taking the old frame down is the
// half that closes the pane holding the command, so a run that cannot outlive
// that pane leaves the window with no frame at all rather than the next one.
//
// The frame verbs take no server flag, so they act on whatever server the
// caller is inside; the test gets a server to itself by giving tmux a private
// directory to keep its default socket in.
func TestMuxOp_FrameCycleFromAFramePane(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := frameCleanupContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	// Two defs, so the rotation has somewhere to go: sorted, "alt" follows
	// "dev" by wrapping around.
	writeFrameDefFile(t, env, "alt", muxOpShellFrameDef)
	writeFrameDefFile(t, env, "dev", muxOpShellFrameDef)

	startMuxOpCommands(t, ctx, env, "api")
	specPath := writeSpecFile(t, `mux:
  layouts:
    - name: solo
      root:
        command: api
`)

	session := "fr"
	cmd := exec.Command("tmux",
		"new-session", "-d", "-s", session, "-n", "sh", "-x", "200", "-y", "50")
	cmd.Env = append(tmuxTmpdirEnv(tmuxTmpdir),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
		cmdman.ENV_CMDMAN_CONF+"="+env.confPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	for _, kv := range [][2]string{
		{cmdman.ENV_CMDMAN_DATA_DIR, env.dataHome},
		{cmdman.ENV_CMDMAN_RUNTIME_DIR, env.runtimeDir},
		{cmdman.ENV_CMDMAN_CONF, env.confPath},
	} {
		tmuxRunWithTmpdir(t, tmuxTmpdir, "set-environment", "-g", kv[0], kv[1])
	}

	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "mux", "up", specPath, "-s", session,
	); err != nil {
		t.Fatalf("mux up: %v\nstderr:\n%s", err, stderr)
	}
	wid := windowIDStamped(t, tmuxTmpdir, session)
	// The frame verbs act on the session's current window, and the driver
	// builds its windows in the background, so the shell window has to go for
	// the dashboard to be the one they find.
	for _, id := range sessionWindowIDs(t, tmuxTmpdir, session) {
		if id != wid {
			tmuxRunWithTmpdir(t, tmuxTmpdir, "kill-window", "-t", id)
		}
	}
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "mux", "frame", "show", "dev", "-s", session,
	); err != nil {
		t.Fatalf("mux frame show: %v\nstderr:\n%s", err, stderr)
	}
	if got := tmuxFormat(t, tmuxTmpdir, wid, "#{@cmdman_frame_def}"); got != "dev" {
		t.Fatalf("the window shows frame %q, want dev before the rotation", got)
	}
	before := readPanes(t, tmuxTmpdir, wid).frame

	framePane := paneWithTitle(t, tmuxTmpdir, wid, "frame-0")
	c := sendMuxOpCommand(t,
		func(args ...string) string { return tmuxRunWithTmpdir(t, tmuxTmpdir, args...) },
		framePane, cmdmanBin+" mux frame cycle")

	// The name of the def going up and the pane realizing it are read together:
	// the window is stamped with the def before its panes are built, so the
	// stamp alone would be satisfied by a replacement that had only started.
	waitForMuxOp(t, "the frame the command was typed in was taken down and not replaced",
		func() string {
			return fmt.Sprintf("%s/%d",
				tmuxFormat(t, tmuxTmpdir, wid, "#{@cmdman_frame_def}"),
				len(readPanes(t, tmuxTmpdir, wid).frame))
		},
		"alt/1", c)

	after := readPanes(t, tmuxTmpdir, wid)
	if len(after.frame) != 1 {
		t.Fatalf("frame panes after the rotation = %v, want the next def's one entry",
			after.frame)
	}
	if slices.Contains(after.frame, framePane) {
		t.Errorf("the pane the command was typed in (%s) is still framing the window; "+
			"the old frame was never taken down", framePane)
	}
	if slices.Equal(before, after.frame) {
		t.Errorf("frame panes are unchanged (%v); the frame was never replaced", before)
	}
	for line := range strings.SplitSeq(tmuxRunWithTmpdir(t, tmuxTmpdir, "list-panes",
		"-t", wid, "-F", "#{pane_id}\t#{pane_dead}"), "\n") {
		if id, dead, ok := strings.Cut(line, "\t"); ok && dead == "1" {
			t.Errorf("pane %s is dead; the replacement left it behind", id)
		}
	}
	if got := tmuxFormat(t, tmuxTmpdir, wid, "#{remain-on-exit}"); got == "on" {
		t.Errorf("the window is still holding dead panes open (remain-on-exit %q)", got)
	}
}

// TestMuxOp_FailureIsReportedAndLogged: an operation that cannot be carried out
// has to say so where the user is, and leave a record where the user can go
// looking.
//
// The failure is a spec naming a command that does not exist, which is refused
// before the window is touched — so the dashboard already standing is also what
// says the refusal cost the user nothing: it is left whole, and the window is
// not left holding dead panes open.
func TestMuxOp_FailureIsReportedAndLogged(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := frameCleanupContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	startMuxOpCommands(t, ctx, env, "api", "worker")
	specPath := writeSpecFile(t, muxOpPairYAML(socket))
	startMuxOpSession(t, env, socket, "work", "shell")

	if _, stderr, err := env.muxExec(ctx, "mux", "up", specPath, "-s", "work"); err != nil {
		t.Fatalf("mux up: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowIDByIdentity(t, socket, "work")

	// Same window, same server — so the failing run is the same operation on the
	// same dashboard — but a layout pointing at a command nobody registered.
	badPath := writeSpecFile(t, fmt.Sprintf(`mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: pair
      root:
        dir: h
        splits: [1, 1]
        panes: [api, ghost]
`, socket))

	stdout, stderr, err := env.muxExec(ctx, "mux", "up", badPath, "-s", "work")
	if err == nil {
		t.Fatalf("the failing spec was accepted; stdout:\n%s", stdout)
	}
	if got := exitStatusOf(t, err); got != 1 {
		t.Fatalf("exit status %d, want 1; stdout:\n%s\nstderr:\n%s", got, stdout, stderr)
	}
	if !strings.Contains(stderr, "ghost") {
		t.Fatalf("the error does not name the command that could not be resolved:\n%s", stderr)
	}

	// The record outlives the run that wrote it: the process carrying an
	// operation out is registered as a command and takes itself off the books on
	// its way out, and the log is kept beside the runtime dir rather than in the
	// directory that goes with it, so it is still there to be read afterwards.
	if logs := muxOpLogs(t, env); !strings.Contains(logs, "ghost") {
		t.Fatalf("no operation log under the runtime dir records the failure:\n%s", logs)
	}
	for _, e := range env.lsJSON(ctx) {
		if name, _ := e["Name"].(string); strings.HasPrefix(name, "muxop-") {
			t.Errorf("the run left the command %q behind for the user to clean up", name)
		}
	}
	if got := windowKeepsDeadPanes(t, socket, wid); got != "off" {
		t.Errorf("the failed run left the window holding dead panes open "+
			"(remain-on-exit %q)", got)
	}
	if got, want := muxOpPaneTitles(t, socket, wid), []string{"api", "worker"}; !slices.Equal(
		got, want,
	) {
		t.Fatalf("the dashboard reads %v after the failed run, want the untouched %v", got, want)
	}
	assertLayoutStamp(t, socket, wid, "0")
}

// TestMuxOp_OutputAndStatusReachTheCaller: an invocation that is not standing
// where the window is rebuilt still reads as the plain command it always was.
// Whatever the operation printed arrives on the caller's own output while the
// caller waits, and the caller exits with the status the operation ended on.
//
// Both places that holds for are covered: outside the multiplexer entirely, and
// in a pane in another window, which the rebuild never reaches.
func TestMuxOp_OutputAndStatusReachTheCaller(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := frameCleanupContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	startMuxOpCommands(t, ctx, env, "api", "worker")
	specPath := writeSpecFile(t, muxOpPairYAML(socket))
	_, shellPane := startMuxOpSession(t, env, socket, "work", "shell")

	// ---- outside the multiplexer ----

	stdout, stderr, err := env.muxExec(ctx, "mux", "up", specPath, "-s", "work")
	if err != nil {
		t.Fatalf("mux up: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "tmux attach -t work") {
		t.Fatalf("the attach hint the operation printed never reached the caller:\n%s", stdout)
	}
	wid := tmuxWindowIDByIdentity(t, socket, "work")

	// ---- from a pane in another window ----

	sender := socketSender(t, socket)
	down := sendMuxOpCommand(t, sender, shellPane,
		fmt.Sprintf("%s mux down %s -s work", cmdmanBin, specPath))

	if got := down.exitStatus(t); got != 0 {
		t.Fatalf("mux down exit status %d, want 0; output:\n%s", got, down.output())
	}
	if !strings.Contains(down.output(), "Restored window") {
		t.Fatalf("what the teardown printed never reached the pane it was typed in:\n%s",
			down.output())
	}
	if got := len(projectPanes(readOpPanes(t, socket, wid))); got != 1 {
		t.Fatalf("the torn-down window holds %d panes, want 1", got)
	}

	// A failing run in the same place carries its status back the same way.
	badPath := writeSpecFile(t, fmt.Sprintf(`mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: solo
      root:
        command: ghost
`, socket))
	bad := sendMuxOpCommand(t, sender, shellPane,
		fmt.Sprintf("%s mux up %s -s work", cmdmanBin, badPath))

	if got := bad.exitStatus(t); got != 1 {
		t.Fatalf("the failing run's exit status is %d, want 1; output:\n%s",
			got, bad.output())
	}
	if !strings.Contains(bad.output(), "ghost") {
		t.Fatalf("the failure the operation reported never reached the pane it was "+
			"typed in:\n%s", bad.output())
	}
}

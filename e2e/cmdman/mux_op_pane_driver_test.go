package cmdman_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman"
)

// The machinery the supervised mux operation tests are written against: a tmux
// server of one's own, a way to type a command into one of its panes and pick
// up what it left behind, and readings of a window that say how far an
// operation got.
//
// It lives apart from the tests because none of it is about any one of them.
// What it is all in aid of is one problem: a command typed into a pane can
// close that pane, taking the shell that would have reported the result with
// it, so the tests read the server instead — and have to wait, since the run
// carries on in a process of its own after the pane is gone.
//
// See mux_op_supervised_test.go for what is asked of it.

// muxOpDeadline bounds every wait here. The run happens in a process of its
// own, which has to be registered, spawned and supervised before it does
// anything, so these waits are for a whole small pipeline rather than a single
// command.
const muxOpDeadline = 45 * time.Second

// muxOpPairYAML lays two commands out side by side on a dedicated tmux server.
// Two panes rather than one so that a rebuild has more than a single step to
// get through, and one layout so that every run of the spec asks for the same
// window and the pane titles say plainly whether it was built.
func muxOpPairYAML(socket string) string {
	return fmt.Sprintf(`mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: pair
      root:
        dir: h
        splits: [1, 1]
        panes: [api, worker]
`, socket)
}

// muxOpShellFrameDef docks a plain shell along the bottom of the window.
//
// A shell is what makes a frame pane somewhere a command can be typed, which is
// how the frame tests reach a pane that lives inside the dashboard without
// hand-splitting one. Three rows because a shorter bar realizes with no usable
// rows underneath the pane border row.
const muxOpShellFrameDef = `frame:
  - edge: bottom
    size: 3
    command: ["/bin/sh"]
`

// writeDefaultFrameConfig puts a shell frame on disk and names it as the frame
// every dashboard comes up inside. Showing that frame is the last thing an up
// does, so its stamp on the window is what says the run reached the end.
func writeDefaultFrameConfig(t *testing.T, e *testEnv, def string) {
	t.Helper()
	writeFrameDefFile(t, e, def, muxOpShellFrameDef)
	// newTestEnv points the config env var at a file that does not exist;
	// writing it is what puts the key in the resolved configuration, and the
	// frame dir writeFrameDefFile used is that file's sibling.
	must(t, os.WriteFile(
		e.confPath, fmt.Appendf(nil, `{"default_frame": %q}`, def), 0o644,
	))
}

// startMuxOpSession adds a session holding a single shell window to this test's
// own tmux server, starting the server on the first call, and returns that
// window's id and the id of its one pane.
//
// The cmdman environment is set on the server as well as on the process that
// starts it: a command typed into a pane finds this test's data and runtime
// dirs only through the environment tmux hands its panes.
func startMuxOpSession(
	t *testing.T,
	e *testEnv,
	socket, session, window string,
) (windowID, paneID string) {
	t.Helper()

	env := [][2]string{
		{cmdman.ENV_CMDMAN_DATA_DIR, e.dataHome},
		{cmdman.ENV_CMDMAN_RUNTIME_DIR, e.runtimeDir},
		{cmdman.ENV_CMDMAN_CONF, e.confPath},
	}
	cmd := exec.Command("tmux", "-L", socket,
		"new-session", "-d", "-s", session, "-n", window, "-x", "200", "-y", "50")
	cmd.Env = hermeticEnviron()
	for _, kv := range env {
		cmd.Env = append(cmd.Env, kv[0]+"="+kv[1])
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session %s: %v\n%s", session, err, strings.TrimSpace(string(out)))
	}
	for _, kv := range env {
		tmuxRun(t, socket, "set-environment", "-g", kv[0], kv[1])
	}

	windowID = tmuxRun(t, socket, "display-message", "-p", "-t", "="+session+":", "#{window_id}")
	paneID = tmuxRun(t, socket, "display-message", "-p", "-t", "="+session+":", "#{pane_id}")
	return windowID, paneID
}

// muxOpCapture is where a command typed into a pane leaves its output and its
// exit status.
//
// Both are files rather than anything read back from the pane, because the pane
// is frequently gone by the time there is anything to read: an operation that
// rebuilds the window it was typed in kills the shell that typed it. Neither
// file is then ever written, which is itself the point — for those runs the
// window is the only witness, and the files serve as diagnostics for a wait
// that times out.
type muxOpCapture struct {
	stdoutPath string
	statusPath string
}

// output returns whatever the invoking shell managed to print, or a note saying
// it printed nothing.
func (c muxOpCapture) output() string {
	b, err := os.ReadFile(c.stdoutPath)
	if err != nil || len(b) == 0 {
		return "(the invoking shell printed nothing; it was most likely closed by " +
			"the operation it started)"
	}
	return string(b)
}

// exitStatus returns the status the invoking shell recorded, waiting for it to
// appear. It is only for invocations expected to outlive their own command.
func (c muxOpCapture) exitStatus(t *testing.T) int {
	t.Helper()
	deadline := time.Now().Add(muxOpDeadline)
	for {
		b, err := os.ReadFile(c.statusPath)
		if err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				code, convErr := strconv.Atoi(s)
				if convErr != nil {
					t.Fatalf("exit status %q is not a number", s)
				}
				return code
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the invoking shell never recorded an exit status; it printed:\n%s",
				c.output())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sendMuxOpCommand types line into pane and runs it, with its output and its
// exit status redirected to files.
//
// send runs one tmux command against the test's server; it is a parameter
// because the frame verbs take no socket flag and so have to be driven on a
// server addressed a different way.
func sendMuxOpCommand(
	t *testing.T,
	send func(args ...string) string,
	pane, line string,
) muxOpCapture {
	t.Helper()
	dir := t.TempDir()
	c := muxOpCapture{
		stdoutPath: filepath.Join(dir, "output"),
		statusPath: filepath.Join(dir, "status"),
	}
	send("send-keys", "-t", pane, "-l", fmt.Sprintf(
		"%s > %s 2>&1; echo $? > %s", line, c.stdoutPath, c.statusPath,
	))
	send("send-keys", "-t", pane, "Enter")
	return c
}

// socketSender adapts the socket-addressed tmux runner to what
// [sendMuxOpCommand] takes.
func socketSender(t *testing.T, socket string) func(args ...string) string {
	return func(args ...string) string { return tmuxRun(t, socket, args...) }
}

// waitForMuxOp polls read until it returns want.
//
// Every assertion about an operation's effect has to wait for it: the run
// happens in a process detached from the pane it was typed in, so the pane's
// prompt coming back — or its pane dying — says nothing about how far the run
// got. A timeout reports the last value alongside whatever the invoking shell
// printed, because a bare deadline says nothing about which half did not
// happen.
func waitForMuxOp(t *testing.T, what string, read func() string, want string, c muxOpCapture) {
	t.Helper()
	deadline := time.Now().Add(muxOpDeadline)
	for {
		got := read()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: last read %q, want %q; the invoking shell printed:\n%s",
				what, got, want, c.output())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// opPane is one pane of a window as these tests read it: whether the frame owns
// it, which layout it was stamped with, and whether tmux is holding it open
// after whatever ran in it exited.
type opPane struct {
	id     string
	marker string
	frame  bool
	dead   bool
}

// readOpPanes reads every pane of windowID. The pane id leads the format
// because the runner trims the whole output, which would otherwise eat an empty
// leading field.
func readOpPanes(t *testing.T, socket, windowID string) []opPane {
	t.Helper()
	out := tmuxRun(t, socket, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{pane_dead}\t#{@cmdman_frame}\t#{@cmdman_marker}")
	var panes []opPane
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		p := opPane{id: fields[0], dead: fields[1] == "1"}
		if len(fields) > 2 {
			p.frame = fields[2] != ""
		}
		if len(fields) > 3 {
			p.marker = fields[3]
		}
		panes = append(panes, p)
	}
	return panes
}

// projectPanes returns the panes the dashboard itself owns — everything the
// frame did not put there.
func projectPanes(panes []opPane) []opPane {
	return slices.DeleteFunc(slices.Clone(panes), func(p opPane) bool { return p.frame })
}

// waitForWindowSettled waits for a window to be left the way a finished run
// leaves it: nothing on screen that only exists because a run is in progress.
//
// A rebuild asks tmux to keep dead panes for its duration, so that a viewer told
// to close does not take its pane down before the rebuild gets to it; on its way
// out it clears the corpses and the setting alike. Both are therefore true
// mid-rebuild, which is why this is a wait and not a plain check — and both are
// exactly what a run that dies with the pane it was typed in leaves behind for
// good.
func waitForWindowSettled(t *testing.T, socket, windowID string, c muxOpCapture) {
	t.Helper()
	waitForMuxOp(t, "window "+windowID+" was left mid-rebuild",
		func() string {
			dead := []string{}
			for _, p := range readOpPanes(t, socket, windowID) {
				if p.dead {
					dead = append(dead, p.id)
				}
			}
			return fmt.Sprintf("dead=%v keep-dead=%s",
				dead, windowKeepsDeadPanes(t, socket, windowID))
		},
		"dead=[] keep-dead=off", c)
}

// windowKeepsDeadPanes reports whether the window is set to hold panes open
// after what ran in them exits.
//
// It is read as a format rather than as a window option because a window that
// was never told either way inherits the answer, and reading the option would
// then report nothing at all rather than the setting in force.
func windowKeepsDeadPanes(t *testing.T, socket, windowID string) string {
	t.Helper()
	return tmuxRun(t, socket, "display-message", "-p", "-t", windowID, "#{remain-on-exit}")
}

// assertLayoutStamp checks that every pane the dashboard owns carries the
// layout stamp — the record of which layout is applied, written on each pane as
// the rebuild finishes with it. An empty want is a window whose dashboard has
// been handed back.
func assertLayoutStamp(t *testing.T, socket, windowID, want string) {
	t.Helper()
	panes := projectPanes(readOpPanes(t, socket, windowID))
	if len(panes) == 0 {
		t.Fatalf("window %s holds no panes of its own", windowID)
	}
	for _, p := range panes {
		if p.marker != want {
			t.Errorf("pane %s carries layout stamp %q, want %q", p.id, p.marker, want)
		}
	}
}

// muxOpPaneTitled returns the id of windowID's pane wearing title.
func muxOpPaneTitled(t *testing.T, socket, windowID, title string) string {
	t.Helper()
	out := tmuxRun(t, socket, "list-panes", "-t", windowID,
		"-F", "#{pane_id}\t#{pane_title}")
	for line := range strings.SplitSeq(out, "\n") {
		if id, got, ok := strings.Cut(line, "\t"); ok && got == title {
			return id
		}
	}
	t.Fatalf("no pane titled %q in window %s; panes:\n%s", title, windowID, out)
	return ""
}

// muxOpPaneTitles returns windowID's pane titles, sorted.
func muxOpPaneTitles(t *testing.T, socket, windowID string) []string {
	t.Helper()
	titles := tmuxPaneField(t, socket, windowID, "#{pane_title}")
	slices.Sort(titles)
	return titles
}

// startMuxOpCommands runs the long-lived commands the layouts point at.
func startMuxOpCommands(t *testing.T, ctx context.Context, e *testEnv, names ...string) {
	t.Helper()
	for _, name := range names {
		e.run(ctx, "run", "-n", name, "--", "/bin/sh", "-c", "sleep 300")
		t.Cleanup(func() { e.cleanupCommand(ctx, name) })
		e.waitForState(ctx, name, "running", defaultTimeout)
	}
}

// muxOpLogs returns the contents of every operation log under the runtime dir.
// The names are built from the window an operation acts on, so they are read
// off the directory rather than predicted.
func muxOpLogs(t *testing.T, e *testEnv) string {
	t.Helper()
	dir := filepath.Join(e.runtimeDir, "mux")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no operation log directory at %s: %v", dir, err)
	}
	var b strings.Builder
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read operation log %s: %v", entry.Name(), err)
		}
		fmt.Fprintf(&b, "--- %s ---\n%s", entry.Name(), body)
	}
	return b.String()
}

// exitStatusOf returns the status a failed cmdman invocation exited with.
func exitStatusOf(t *testing.T, err error) int {
	t.Helper()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("error %v is not a process exit", err)
	}
	return exitErr.ExitCode()
}

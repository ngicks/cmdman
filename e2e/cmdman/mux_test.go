package cmdman_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// These e2e tests drive the real `cmdman mux` / `cmdman compose mux` binary
// against a real tmux server, exercising the full CLI path that the unit and
// pkg/muxctl/tmux integration tests do not cover end to end: YAML decode →
// leaf-name resolution against the running cmdman service → argv build with
// --data-dir/--runtime-dir threading → driver autodetect → tmux window/pane
// realization → layout cycling → the "mux is a disposable viewer" guarantee.
//
// Each test uses a tmux server on a dedicated -L socket (set via the spec's
// driver_opt.socket) so it is isolated from any tmux the developer is running
// and from sibling tests, which run in parallel.

// requireTmux skips the test when tmux is not installed. mux is the only
// feature in the e2e suite that needs an external multiplexer, so a missing
// tmux skips just these tests rather than failing the whole suite.
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH; skipping mux e2e test")
	}
}

// muxSocket derives a unique tmux -L socket name from the test name so the
// parallel mux tests never share a server.
func muxSocket(t *testing.T) string {
	t.Helper()
	return "cmdman-e2e-" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
}

// tmuxRun shells out to `tmux -L <socket> <args...>` and returns trimmed
// combined output, failing the test on error.
func tmuxRun(t *testing.T, socket string, args ...string) string {
	t.Helper()
	full := append([]string{"-L", socket}, args...)
	out, err := exec.Command("tmux", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v\n%s", strings.Join(full, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// killTmuxServer tears down the per-test tmux server, ignoring errors (the
// server may already be gone, e.g. a test that kills it itself).
func killTmuxServer(t *testing.T, socket string) {
	t.Helper()
	_ = exec.Command("tmux", "-L", socket, "kill-server").Run()
}

// tmuxWindowID returns the @id of the (unique) window named windowName across
// every session on the socket. The cmdman-owned window is created detached and
// is never the session's active window, so it must be targeted by id, not by
// the bare session name.
func tmuxWindowID(t *testing.T, socket, windowName string) string {
	t.Helper()
	out := tmuxRun(t, socket, "list-windows", "-a", "-F", "#{window_name}\t#{window_id}")
	for line := range strings.SplitSeq(out, "\n") {
		name, id, ok := strings.Cut(line, "\t")
		if ok && name == windowName {
			return id
		}
	}
	t.Fatalf("window %q not found on socket %s; windows:\n%s", windowName, socket, out)
	return ""
}

// tmuxPaneField returns the given format field for every pane in windowID, in
// tmux's list order.
func tmuxPaneField(t *testing.T, socket, windowID, field string) []string {
	t.Helper()
	out := tmuxRun(t, socket, "list-panes", "-t", windowID, "-F", field)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// windowPaneBases returns the sorted pane border titles of windowID. The layout
// marker lives in the @cmdman_marker per-pane option, so titles are the plain
// pane names.
func windowPaneBases(t *testing.T, socket, windowID string) []string {
	t.Helper()
	titles := tmuxPaneField(t, socket, windowID, "#{pane_title}")
	bases := slices.Clone(titles)
	slices.Sort(bases)
	return bases
}

// windowMarker returns the layout marker shared by every pane in windowID (read
// from the @cmdman_marker per-pane option), failing the test if the panes
// disagree (which would mean ApplyLayout did not tag them uniformly). A pane
// with no marker option yields -1.
func windowMarker(t *testing.T, socket, windowID string) int {
	t.Helper()
	values := tmuxPaneField(t, socket, windowID, "#{@cmdman_marker}")
	if len(values) == 0 {
		t.Fatalf("window %s has no panes", windowID)
	}
	marker := -2
	for _, v := range values {
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
			t.Fatalf("inconsistent layout markers across panes: %v", values)
		}
	}
	return marker
}

// muxExec runs the cmdman binary like testEnv.exec, but muxless, so the mux
// driver deterministically takes the "outside a multiplexer" path (build
// detached + print the attach hint) regardless of whether the test process
// itself is running inside tmux. The target server is still the dedicated
// driver_opt.socket from the spec.
func (e *testEnv) muxExec(ctx context.Context, args ...string) (string, string, error) {
	return e.muxExecInDir(ctx, "", args...)
}

// muxExecInDir is muxExec with the child process run in dir ("" inherits the
// test process cwd). A controlled cwd exercises `compose mux`'s no-`-f`
// auto-selection of the cwd compose file.
func (e *testEnv) muxExecInDir(
	ctx context.Context,
	dir string,
	args ...string,
) (string, string, error) {
	e.t.Helper()
	res := e.Cmd(args...).InDir(dir).Muxless().Exec(ctx)
	return res.Stdout, res.Stderr, res.Err
}

// tmuxTmpdirEnv returns a copy of the current environment with the inherited
// TMUX=, TMUX_PANE=, ZELLIJ=, and TMUX_TMPDIR= entries stripped and TMUX_TMPDIR
// set to tmuxTmpdir. Stripping TMUX= (and ZELLIJ=) is what keeps a flagless
// `tmux` invocation from ever reaching the enclosing interactive server: tmux
// resolves its target from $TMUX ahead of TMUX_TMPDIR, so leaving $TMUX in
// place would let a bare `tmux kill-server` kill the developer's real session.
//
// TMUX_PANE goes for a related reason: a teardown reads it to tell whether it is
// running inside the window it was asked to close, and a pane id inherited from
// the developer's own tmux names a pane on the test's server just as readily —
// pane ids are small numbers, and both servers start counting at the same place.
func tmuxTmpdirEnv(tmuxTmpdir string) []string {
	env := slices.DeleteFunc(hermeticEnviron(), func(s string) bool {
		return strings.HasPrefix(s, "TMUX=") ||
			strings.HasPrefix(s, "TMUX_PANE=") ||
			strings.HasPrefix(s, "ZELLIJ=") ||
			strings.HasPrefix(s, "TMUX_TMPDIR=")
	})
	return append(env, "TMUX_TMPDIR="+tmuxTmpdir)
}

// muxExecWithTmpdir is like muxExec but also sets TMUX_TMPDIR to tmuxTmpdir in
// the child's environment. This redirects the default tmux socket path so tests
// that need to use the default socket (e.g. `mux ls`, which has no --socket
// flag) can still run in isolation: every test uses its own TMUX_TMPDIR.
func (e *testEnv) muxExecWithTmpdir(
	ctx context.Context,
	tmuxTmpdir string,
	args ...string,
) (string, string, error) {
	e.t.Helper()
	res := e.Cmd(args...).WithTmuxTmpdir(tmuxTmpdir).Exec(ctx)
	return res.Stdout, res.Stderr, res.Err
}

// tmuxRunWithTmpdir is like tmuxRun but sets TMUX_TMPDIR so the command finds
// the same default-socket server as muxExecWithTmpdir.
func tmuxRunWithTmpdir(t *testing.T, tmuxTmpdir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("tmux", args...)
	cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}

// killDefaultTmuxServer kills the tmux default-socket server rooted at
// tmuxTmpdir, ignoring errors (server may already be gone).
func killDefaultTmuxServer(t *testing.T, tmuxTmpdir string) {
	t.Helper()
	cmd := exec.Command("tmux", "kill-server")
	cmd.Env = tmuxTmpdirEnv(tmuxTmpdir)
	_ = cmd.Run()
}

// standaloneMuxYAML is a single-layout spec with three side-by-side panes
// bound to commands api/worker/cache, on the given dedicated tmux socket.
func standaloneMuxYAML(socket string) string {
	return fmt.Sprintf(`mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: services
      root:
        dir: h
        splits: [1, 1, 1]
        panes:
          - api
          - worker
          - cache
`, socket)
}

// TestMux_BuildsPanesBoundToCommands runs `cmdman mux <file>` against three
// real commands and verifies the resulting tmux window: one pane per command,
// border titles = command names, each pane running the resolved
// `cmdman ... attach <id>` viewer with the data dir threaded through, and the
// outside-a-multiplexer attach hint printed to stdout.
func TestMux_BuildsPanesBoundToCommands(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	ids := map[string]string{}
	for _, name := range []string{"api", "worker", "cache"} {
		env.run(ctx, "run", "-n", name, "--", "/bin/sh", "-c", "sleep 300")
		t.Cleanup(func() { env.cleanupCommand(ctx, name) })
		env.waitForState(ctx, name, "running", defaultTimeout)
		ids[name] = env.inspectJSON(ctx, name)["ID"].(string)
	}

	specPath := writeSpecFile(t, standaloneMuxYAML(socket))

	stdout, stderr, err := env.muxExec(ctx, "mux", specPath)
	if err != nil {
		t.Fatalf("cmdman mux failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "tmux attach -t cmdman") {
		t.Fatalf("expected attach hint on stdout; got:\n%s", stdout)
	}

	wid := tmuxWindowID(t, socket, "cmdman")

	if got, want := windowPaneBases(
		t,
		socket,
		wid,
	), []string{
		"api",
		"cache",
		"worker",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Fatalf("pane base names = %v, want %v", got, want)
	}
	if got := windowMarker(t, socket, wid); got != 0 {
		t.Fatalf("first apply marker = %d, want 0", got)
	}

	// Each pane runs the resolved `attach <id>` viewer with --data-dir
	// threaded; together they cover all three resolved command IDs.
	starts := tmuxPaneField(t, socket, wid, "#{pane_start_command}")
	if len(starts) != 3 {
		t.Fatalf("want 3 panes, got %d: %v", len(starts), starts)
	}
	for _, s := range starts {
		if !strings.Contains(s, "attach") {
			t.Errorf("pane start command is not an attach: %q", s)
		}
		if !strings.Contains(s, "--data-dir "+env.dataHome) {
			t.Errorf("pane start command does not thread --data-dir %q: %q", env.dataHome, s)
		}
	}
	joined := strings.Join(starts, "\n")
	for name, id := range ids {
		if !strings.Contains(joined, id) {
			t.Errorf("no pane runs attach for %s (id %s); pane commands:\n%s", name, id, joined)
		}
	}
}

// TestMux_AttachPaneExposesApplicationMouseFlags verifies the same machinery
// plain tmux uses for neovim-style mouse pass-through: once the in-pane
// application emits mouse-enable sequences, tmux marks the pane with
// mouse_any_flag / mouse_sgr_flag so its default Mouse*Pane bindings choose
// send-keys -M instead of handling the event as a tmux action.
func TestMux_AttachPaneExposesApplicationMouseFlags(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	env.run(
		ctx,
		"run",
		"-t",
		"-n",
		"mouseapp",
		"--scrollback-bytes",
		"64",
		"--",
		"/bin/sh",
		"-c",
		strings.Join([]string{
			`printf '\033[?1000h\033[?1006h\033[?2004h'`,
			`i=0`,
			`while [ "$i" -lt 20 ]; do`,
			`echo "filler-$i-filler-$i-filler-$i"`,
			`i=$((i+1))`,
			`done`,
			`sleep 300`,
		}, "\n"),
	)
	t.Cleanup(func() { env.cleanupCommand(ctx, "mouseapp") })
	env.waitForState(ctx, "mouseapp", "running", defaultTimeout)

	specPath := writeSpecFile(t, fmt.Sprintf(`mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: editor
      root:
        command: mouseapp
`, socket))

	if _, stderr, err := env.muxExec(ctx, "mux", specPath); err != nil {
		t.Fatalf("cmdman mux failed: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowID(t, socket, "cmdman")

	deadline := time.Now().Add(3 * time.Second)
	var last []string
	for time.Now().Before(deadline) {
		last = tmuxPaneField(
			t,
			socket,
			wid,
			"#{mouse_any_flag}\t#{mouse_sgr_flag}\t#{pane_in_mode}",
		)
		if len(last) == 1 && last[0] == "1\t1\t0" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pane mouse flags never reflected application mouse mode; last=%v", last)
}

// cycleMuxYAML has two layouts of different shape: "wide" (two panes) and
// "solo" (a single-leaf root). Re-running mux must advance the embedded marker
// and switch the window to the next layout.
func cycleMuxYAML(socket string) string {
	return fmt.Sprintf(`mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: wide
      root:
        dir: h
        splits: [1, 1]
        panes: [api, worker]
    - name: solo
      root:
        command: api
`, socket)
}

// TestMux_CyclesToNextLayoutOnRerun verifies the consumer-side layout cycle:
// the cmdman layer reads the previously-applied marker back via StatWindow and
// applies (prev+1) mod len(layouts) on the next run. The two layouts differ in
// pane count, so a successful cycle is observable as both a marker bump (0→1)
// and a window rebuild (2 panes → 1 pane).
func TestMux_CyclesToNextLayoutOnRerun(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	for _, name := range []string{"api", "worker"} {
		env.run(ctx, "run", "-n", name, "--", "/bin/sh", "-c", "sleep 300")
		t.Cleanup(func() { env.cleanupCommand(ctx, name) })
		env.waitForState(ctx, name, "running", defaultTimeout)
	}

	specPath := writeSpecFile(t, cycleMuxYAML(socket))

	if _, stderr, err := env.muxExec(ctx, "mux", specPath); err != nil {
		t.Fatalf("first cmdman mux failed: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowID(t, socket, "cmdman")
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 2 {
		t.Fatalf("after first run want 2 panes, got %d", got)
	}
	if got := windowMarker(t, socket, wid); got != 0 {
		t.Fatalf("after first run marker = %d, want 0", got)
	}

	if _, stderr, err := env.muxExec(ctx, "mux", specPath); err != nil {
		t.Fatalf("second cmdman mux failed: %v\nstderr:\n%s", err, stderr)
	}
	if got := tmuxWindowID(t, socket, "cmdman"); got != wid {
		t.Fatalf(
			"window id drifted across runs: %s vs %s (should reuse the owned window)",
			wid,
			got,
		)
	}
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 1 {
		t.Fatalf("after second run want 1 pane, got %d", got)
	}
	if got := windowMarker(t, socket, wid); got != 1 {
		t.Fatalf("after second run marker = %d, want 1 (cycle did not advance)", got)
	}
}

// singleMuxYAML is a one-pane layout bound to a single command "solo".
func singleMuxYAML(socket string) string {
	return fmt.Sprintf(`mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: only
      root:
        command: solo
`, socket)
}

// TestMux_KillingSessionLeavesCommandRunning asserts the plan's load-bearing
// guiding principle: the multiplexer is a disposable viewer, never the source
// of truth. Tearing down the whole tmux server (which SIGHUPs the in-pane
// `cmdman attach`) must not stop — nor restart — the underlying command, which
// the cmdman daemon owns independently of tmux.
func TestMux_KillingSessionLeavesCommandRunning(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	const title = "still-supervised"
	env.run(
		ctx,
		"run", "-t", "-n", "solo", "--", "/bin/sh", "-c",
		"printf '\033]2;"+title+"\007'; sleep 300",
	)
	t.Cleanup(func() { env.cleanupCommand(ctx, "solo") })
	env.waitForState(ctx, "solo", "running", defaultTimeout)
	pidBefore := env.livePID(ctx, "solo")

	specPath := writeSpecFile(t, singleMuxYAML(socket))
	if _, stderr, err := env.muxExec(ctx, "mux", specPath); err != nil {
		t.Fatalf("cmdman mux failed: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowID(t, socket, "cmdman")
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 1 {
		t.Fatalf("want 1 pane before kill, got %d", got)
	}
	waitForPaneTitle(t, socket, wid, title, 3*time.Second)

	killTmuxServer(t, socket)
	// Let any SIGHUP-driven teardown that would (wrongly) reach the command
	// have time to land before we assert it did not.
	time.Sleep(500 * time.Millisecond)

	info := env.inspectJSON(ctx, "solo")
	if info["State"] != "running" {
		t.Fatalf(
			"killing the mux session changed command state to %v; mux must be view-only",
			info["State"],
		)
	}
	if pidAfter := env.livePID(ctx, "solo"); pidAfter != pidBefore {
		t.Fatalf(
			"command pid changed across mux teardown (was %v, now %v): restarted, not left alone",
			pidBefore,
			pidAfter,
		)
	}

	// A fresh viewer receives the monitor's current terminal title even though
	// the command emitted OSC 2 only once, before the first viewer attached.
	if _, stderr, err := env.muxExec(ctx, "mux", specPath); err != nil {
		t.Fatalf("cmdman mux after server restart failed: %v\nstderr:\n%s", err, stderr)
	}
	wid = tmuxWindowID(t, socket, "cmdman")
	waitForPaneTitle(t, socket, wid, title, 3*time.Second)
}

// livePID returns the live OS pid of a running command from `cmdman inspect`.
func (e *testEnv) livePID(ctx context.Context, idOrName string) float64 {
	e.t.Helper()
	info := e.inspectJSON(ctx, idOrName)
	live, _ := info["LiveStatus"].(map[string]any)
	if live == nil {
		e.t.Fatalf("no live_status for %q; command not running?\n%v", idOrName, info)
	}
	pid, _ := live["PID"].(float64)
	if pid <= 0 {
		e.t.Fatalf("bad live pid for %q: %v", idOrName, live["PID"])
	}
	return pid
}

// composeMuxYAML is a compose file carrying both two long-running services and
// an embedded mux: section that lays them out side by side.
func composeMuxYAML(project, socket string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, "300"]
  beta:
    args: [sleep, "300"]
mux:
  driver:
    name: tmux
    socket: %s
  layouts:
    - name: services
      root:
        dir: h
        splits: [1, 1]
        panes: [alpha, beta]
`, project, socket)
}

// TestComposeMux_BuildsPanesForServices runs `cmdman compose mux`, which reads
// the mux: section embedded in the compose file, resolves each leaf's service
// name to its backing command id, and builds the project-named window
// (cmdman-<project>) with one pane per service.
func TestComposeMux_BuildsPanesForServices(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "muxcompose"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(t, wd, composeMuxYAML(project, socket))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	if _, stderr, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"up",
	); err != nil {
		t.Fatalf("compose up failed: %v\nstderr:\n%s", err, stderr)
	}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", defaultTimeout)
	}

	stdout, stderr, err := env.muxExec(ctx, "compose", "--workdir", wd, "-f", composePath, "mux")
	if err != nil {
		t.Fatalf("compose mux failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "tmux attach -t cmdman") {
		t.Fatalf("expected attach hint on stdout; got:\n%s", stdout)
	}

	// compose mux names the owned window cmdman-<project>. Unpinned compose
	// leaves are cycle-scale targets and resolve at replica position 1, so
	// panes are titled <command>-1.
	wid := tmuxWindowID(t, socket, "cmdman-"+project)
	if got, want := windowPaneBases(
		t,
		socket,
		wid,
	), []string{
		"alpha-1",
		"beta-1",
	}; !slices.Equal(
		got,
		want,
	) {
		t.Fatalf("pane base names = %v, want %v", got, want)
	}
}

// TestComposeMux_MissingSectionErrors verifies that `cmdman compose mux` against
// a compose file with no mux: section is a hard error (no synthesized default),
// per the plan.
func TestComposeMux_MissingSectionErrors(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "muxmissing"
	composePath := writeComposeFile(t, wd, composeBasicYAML(project))

	stdout, stderr := env.runExpectFail(ctx, "compose", "--workdir", wd, "-f", composePath, "mux")
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "mux:") || !strings.Contains(combined, "missing") {
		t.Fatalf("expected a missing-mux-section error; got stdout=%q stderr=%q", stdout, stderr)
	}
}

// TestComposeMux_NoFileAutoSelectsCwdFile verifies the headline behavior: from a
// project directory holding a cmd-compose.yaml with a mux: section, `cmdman
// compose mux` needs neither -f nor --workdir. The cwd compose file is the sole
// mux compose associated with the directory, so it is unambiguous and gets
// auto-selected.
func TestComposeMux_NoFileAutoSelectsCwdFile(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "muxauto"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(t, wd, composeMuxYAML(project, socket))
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

	// The behavior under test: no -f, no --workdir, just run from the project dir.
	stdout, stderr, err := env.muxExecInDir(ctx, wd, "compose", "mux")
	if err != nil {
		t.Fatalf("compose mux (no -f) failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "tmux attach -t cmdman") {
		t.Fatalf("expected attach hint on stdout; got:\n%s", stdout)
	}
	wid := tmuxWindowID(t, socket, "cmdman-"+project)
	if got, want := windowPaneBases(t, socket, wid), []string{
		"alpha-1", "beta-1",
	}; !slices.Equal(got, want) {
		t.Fatalf("pane base names = %v, want %v", got, want)
	}
}

// TestComposeMux_NoFileNoneAssociatedErrors verifies that `cmdman compose mux`
// with no -f, run from a directory with no associated mux compose, fails asking
// for -f rather than guessing.
func TestComposeMux_NoFileNoneAssociatedErrors(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)

	stdout, stderr, err := env.muxExecInDir(ctx, wd, "compose", "mux")
	if err == nil {
		t.Fatalf("expected failure; stdout=%q stderr=%q", stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "associated with this directory") ||
		!strings.Contains(combined, "-f") {
		t.Fatalf("expected a 'no associated mux compose' error asking for -f; "+
			"got stdout=%q stderr=%q", stdout, stderr)
	}
}

// TestComposeMux_DownFindsWindowServerWide is the core capability test for the
// new server-wide discovery: build a compose dashboard in session A, then run
// `compose mux down` from OUTSIDE tmux (no $TMUX — muxExec strips it). The
// window must be found by its identity stamp and restored even though the
// caller has no $TMUX context pointing at the right session.
func TestComposeMux_DownFindsWindowServerWide(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "muxservwide"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(t, wd, composeMuxYAML(project, socket))
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
		t.Fatalf("compose mux failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 2 {
		t.Fatalf("want 2 panes before down, got %d", got)
	}

	// Run compose mux down from OUTSIDE tmux (muxExec strips $TMUX). The window
	// must be found by its identity stamp without any $TMUX context.
	downStdout, downStderr, downErr := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "down",
	)
	if downErr != nil {
		t.Fatalf(
			"compose mux down failed: %v\nstdout:\n%s\nstderr:\n%s",
			downErr, downStdout, downStderr,
		)
	}
	if !strings.Contains(downStdout, "Restored window") {
		t.Fatalf("expected 'Restored window ...' on stdout; got:\n%s", downStdout)
	}
	if !strings.Contains(downStdout, window) {
		t.Fatalf("expected window name %q in down output; got:\n%s", window, downStdout)
	}

	if got := tmuxWindowID(t, socket, window); got != wid {
		t.Fatalf("window id changed across down: %s vs %s", wid, got)
	}
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_id}")); got != 1 {
		t.Fatalf("want 1 pane after down, got %d", got)
	}
	if got := tmuxWindowOption(t, socket, wid, "@cmdman_window"); got != "" {
		t.Errorf("@cmdman_window still set after down: %q", got)
	}
}

// tmuxWindowIDByIdentity returns the @id of the window whose @cmdman_window
// ownership option equals identity, across every session on the socket, failing
// unless exactly one window carries it. Same-named windows are precisely what
// the stamp exists to tell apart, so tmuxWindowID's name lookup cannot answer
// for a project whose window shares its name with another's.
func tmuxWindowIDByIdentity(t *testing.T, socket, identity string) string {
	t.Helper()
	out := tmuxRun(t, socket, "list-windows", "-a", "-F", "#{window_id}\t#{@cmdman_window}")
	var found []string
	for line := range strings.SplitSeq(out, "\n") {
		id, stamp, ok := strings.Cut(line, "\t")
		if ok && stamp == identity {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		t.Fatalf("windows stamped %q = %v, want exactly one; windows on %s:\n%s",
			identity, found, socket, out)
	}
	return found[0]
}

// tmuxWindowName returns the current name of windowID.
func tmuxWindowName(t *testing.T, socket, windowID string) string {
	t.Helper()
	return tmuxRun(t, socket, "display-message", "-p", "-t", windowID, "#{window_name}")
}

// TestComposeMux_SameProjectNameInTwoWorkdirs is the two-checkouts case: one
// project name, two work directories — a repository cloned twice, or a project
// and its worktree. Both dashboards want the window named cmdman-<project>, and
// the window name is where their claims collide; the ownership stamp, hashed
// from the work directory too, is where they do not.
//
// So each `compose mux up` must build a window of its own, and a `compose mux
// down` in one directory must tear down that directory's dashboard and leave the
// other's standing.
func TestComposeMux_SameProjectNameInTwoWorkdirs(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	const project = "muxtwice"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	// Two projects that differ in nothing a window name can show.
	type site struct {
		workdir     string
		composePath string
		identity    string
		windowID    string
	}
	sites := make([]*site, 2)
	for i := range sites {
		wd := composeWorkdir(t)
		sites[i] = &site{
			workdir:     wd,
			composePath: writeComposeFile(t, wd, composeMuxYAML(project, socket)),
			identity: compose.ProjectSelection{
				WorkDir: wd,
				Project: project,
			}.ProjectIdentity(),
		}
		t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	}
	if sites[0].identity == sites[1].identity {
		t.Fatalf("both work directories hash to identity %q", sites[0].identity)
	}

	for _, s := range sites {
		if _, stderr, err := env.exec(
			ctx, "compose", "--workdir", s.workdir, "-f", s.composePath, "up",
		); err != nil {
			t.Fatalf("compose up in %s failed: %v\nstderr:\n%s", s.workdir, err, stderr)
		}
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.workdir="+s.workdir,
			"-l", "cmdman.compose.project="+project,
		) {
			env.waitForState(ctx, e["ID"].(string), "running", defaultTimeout)
		}

		stdout, stderr, err := env.muxExec(
			ctx, "compose", "--workdir", s.workdir, "-f", s.composePath, "mux",
		)
		if err != nil {
			t.Fatalf("compose mux in %s failed: %v\nstdout:\n%s\nstderr:\n%s",
				s.workdir, err, stdout, stderr)
		}
		s.windowID = tmuxWindowIDByIdentity(t, socket, s.identity)
	}

	first, second := sites[0], sites[1]
	if first.windowID == second.windowID {
		t.Fatalf("both projects took window %s; the second up stole the first's dashboard",
			first.windowID)
	}
	window := "cmdman-" + project
	for _, s := range sites {
		if got := tmuxWindowName(t, socket, s.windowID); got != window {
			t.Errorf("window %s is named %q, want %q", s.windowID, got, window)
		}
		if got := len(tmuxPaneField(t, socket, s.windowID, "#{pane_id}")); got != 2 {
			t.Errorf("window %s has %d panes, want the layout's 2", s.windowID, got)
		}
	}

	// Down in the first directory only.
	downStdout, downStderr, downErr := env.muxExec(
		ctx, "compose", "--workdir", first.workdir, "-f", first.composePath, "mux", "down",
	)
	if downErr != nil {
		t.Fatalf("compose mux down failed: %v\nstdout:\n%s\nstderr:\n%s",
			downErr, downStdout, downStderr)
	}
	if !strings.Contains(downStdout, "Restored window") {
		t.Fatalf("expected a restored-window line on stdout; got:\n%s", downStdout)
	}

	if got := len(tmuxPaneField(t, socket, first.windowID, "#{pane_id}")); got != 1 {
		t.Errorf("torn-down window %s has %d panes, want 1", first.windowID, got)
	}
	if got := tmuxWindowOption(t, socket, first.windowID, "@cmdman_window"); got != "" {
		t.Errorf("@cmdman_window on the torn-down window = %q, want it cleared", got)
	}

	// The other directory's dashboard is untouched, stamp and all — it is still
	// the one its own down, land and cycle will find.
	if got := len(tmuxPaneField(t, socket, second.windowID, "#{pane_id}")); got != 2 {
		t.Errorf("the other project's window %s has %d panes, want its 2 left standing",
			second.windowID, got)
	}
	if got := tmuxWindowOption(
		t, socket, second.windowID, "@cmdman_window",
	); got != second.identity {
		t.Errorf("@cmdman_window on the other project's window = %q, want %q",
			got, second.identity)
	}
}

// muxLsYAML is a one-pane spec that uses the default tmux socket (no
// driver_opt.socket). Used by ls tests, which drive `mux ls` without a spec
// file: both `mux up` and `mux ls` must therefore talk to the same server. The
// calling test redirects the default socket via TMUX_TMPDIR.
func muxLsYAML() string {
	return `mux:
  driver:
    name: tmux
  layouts:
    - name: only
      root:
        command: solo
`
}

// composeMuxLsYAML is like composeMuxYAML but omits driver_opt.socket, so
// `compose mux up` and `compose mux ls` both hit the default tmux socket
// (redirected per-test via TMUX_TMPDIR). Tests that exercise the driver_opt
// passthrough on a custom socket use composeMuxCustomSocketYAML instead.
func composeMuxLsYAML(project string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, "300"]
  beta:
    args: [sleep, "300"]
mux:
  driver:
    name: tmux
  layouts:
    - name: services
      root:
        dir: h
        splits: [1, 1]
        panes: [alpha, beta]
`, project)
}

// TestMuxLs_ListsDashboard builds a standalone dashboard via the default tmux
// socket (isolated per-test via TMUX_TMPDIR) and verifies that `cmdman mux ls`
// reports it with the correct SESSION and IDENTITY values. The table format is
// asserted via --format for stable, whitespace-independent matching.
//
// Note: `mux ls` has no spec-file argument and therefore no way to receive a
// custom socket; both `mux up` and `mux ls` must target the same socket. We
// achieve isolation by redirecting TMUX_TMPDIR to a private temp directory so
// the default socket (`$TMUX_TMPDIR/tmux-<uid>/default`) is unique per test.
func TestMuxLs_ListsDashboard(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	// Unique TMUX_TMPDIR gives this test its own default tmux socket.
	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	env.run(ctx, "run", "-n", "solo", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, "solo") })
	env.waitForState(ctx, "solo", "running", defaultTimeout)

	specPath := writeSpecFile(t, muxLsYAML())

	// Outside tmux: session = "cmdman", window = "cmdman", identity = "cmdman".
	if _, stderr, err := env.muxExecWithTmpdir(ctx, tmuxTmpdir, "mux", specPath); err != nil {
		t.Fatalf("cmdman mux failed: %v\nstderr:\n%s", err, stderr)
	}

	tmuxRunWithTmpdir(t, tmuxTmpdir,
		"list-windows", "-a", "-F", "#{window_name}\t#{window_id}",
	)

	stdout, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"mux", "ls", "--format", "{{.SessionName}}\t{{.Identity}}",
	)
	if err != nil {
		t.Fatalf("cmdman mux ls failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Outside tmux the session defaults to "cmdman" and the standalone identity
	// equals the window name which defaults to the session name: "cmdman".
	const wantSession = "cmdman"
	const wantIdentity = "cmdman"
	found := false
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && parts[0] == wantSession && parts[1] == wantIdentity {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf(
			"mux ls: expected a row with session=%q identity=%q; got:\n%s",
			wantSession, wantIdentity, stdout,
		)
	}
}

// TestComposeMuxLs_ListsDashboard builds a compose dashboard via the default
// tmux socket (isolated per-test via TMUX_TMPDIR) and verifies that
// `compose mux ls` reports it with the correct SESSION and IDENTITY values.
// This test covers the default-socket path; TestComposeMuxLs_HonorsDriverOpt
// covers the custom-socket path that was fixed in the 2026-06-12 decision.
func TestComposeMuxLs_ListsDashboard(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	// Unique TMUX_TMPDIR gives this test its own default tmux socket.
	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })

	wd := composeWorkdir(t)
	project := "muxls"
	composePath := writeComposeFile(t, wd, composeMuxLsYAML(project))
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
		t.Fatalf("compose mux failed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir,
		"compose", "--workdir", wd, "-f", composePath,
		"mux", "ls", "--format", "{{.SessionName}}\t{{.Identity}}",
	)
	if err != nil {
		t.Fatalf("compose mux ls failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Outside tmux the session defaults to "cmdman". The compose identity is
	// <wdhash>-<project>; we verify the suffix rather than predicting the hash.
	// escapeName("muxls") = "muxls" (no dashes to escape).
	const wantSession = "cmdman"
	identitySuffix := "-" + project
	found := false
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 &&
			parts[0] == wantSession &&
			strings.HasSuffix(parts[1], identitySuffix) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf(
			"compose mux ls: expected a row with session=%q and identity suffix %q; got:\n%s",
			wantSession, identitySuffix, stdout,
		)
	}
}

// TestMux_RootAliasEqualsUp verifies that `cmdman mux <path>` (root alias) is
// equivalent to `cmdman mux up <path>`: both build the same dashboard on the
// same socket. Each sub-test uses its own socket for isolation.
func TestMux_RootAliasEqualsUp(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "solo", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, "solo") })
	env.waitForState(ctx, "solo", "running", defaultTimeout)

	for _, tc := range []struct {
		name string
		args func(specPath string) []string
	}{
		{
			name: "root-alias",
			args: func(specPath string) []string {
				return []string{"mux", specPath}
			},
		},
		{
			name: "explicit-up",
			args: func(specPath string) []string {
				return []string{"mux", "up", specPath}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			socket := muxSocket(t)
			t.Cleanup(func() { killTmuxServer(t, socket) })

			specPath := writeSpecFile(t, singleMuxYAML(socket))

			stdout, stderr, err := env.muxExec(ctx, tc.args(specPath)...)
			if err != nil {
				t.Fatalf("cmdman %s failed: %v\nstdout:\n%s\nstderr:\n%s",
					tc.name, err, stdout, stderr)
			}
			if !strings.Contains(stdout, "tmux attach -t cmdman") {
				t.Fatalf("expected attach hint; got:\n%s", stdout)
			}
			wid := tmuxWindowID(t, socket, "cmdman")
			if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 1 {
				t.Fatalf("want 1 pane, got %d", got)
			}
		})
	}
}

// writeSpecFile writes a standalone mux layout spec to a temp file and returns
// its path.
func writeSpecFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mux.yaml")
	must(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// tmuxWindowOption returns the window-scoped value of a tmux option, tolerating
// errors by returning "" (an unset option is what the detach assertions check).
func tmuxWindowOption(t *testing.T, socket, windowID, name string) string {
	t.Helper()
	out, err := exec.Command(
		"tmux", "-L", socket,
		"show-options", "-w", "-t", windowID, "-v", name,
	).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestMux_DetachRestoresWindowAndKeepsCommands runs `cmdman mux <file>` then
// `cmdman mux --detach <file>` and verifies the full detach path: the window
// survives but collapses to a single clean pane, the tmux options cmdman set
// (pane-border-status, @cmdman_marker) are cleared, and — the load-bearing
// disposable-viewer guarantee — every supervised command is still running with
// an unchanged pid.
func TestMux_DetachRestoresWindowAndKeepsCommands(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	names := []string{"api", "worker", "cache"}
	pids := map[string]float64{}
	for _, name := range names {
		env.run(ctx, "run", "-n", name, "--", "/bin/sh", "-c", "sleep 300")
		t.Cleanup(func() { env.cleanupCommand(ctx, name) })
		env.waitForState(ctx, name, "running", defaultTimeout)
		pids[name] = env.livePID(ctx, name)
	}

	specPath := writeSpecFile(t, standaloneMuxYAML(socket))

	if _, stderr, err := env.muxExec(ctx, "mux", specPath); err != nil {
		t.Fatalf("cmdman mux failed: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowID(t, socket, "cmdman")
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 3 {
		t.Fatalf("want 3 panes before detach, got %d", got)
	}
	if got := tmuxWindowOption(t, socket, wid, "pane-border-status"); got != "top" {
		t.Fatalf("pane-border-status before detach = %q, want top", got)
	}

	{
		stdout, stderr, err := env.muxExec(ctx, "mux", "down", specPath)
		if err != nil {
			t.Fatalf("cmdman mux down failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "Restored window") {
			t.Fatalf("expected 'Restored window ...' on stdout; got:\n%s", stdout)
		}
	}

	if got := tmuxWindowID(t, socket, "cmdman"); got != wid {
		t.Fatalf("window id changed across detach: %s vs %s", wid, got)
	}
	// Count by pane_id, not pane_title: detach clears the restored pane's title.
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_id}")); got != 1 {
		t.Fatalf("want 1 pane after detach, got %d", got)
	}
	for _, m := range tmuxPaneField(t, socket, wid, "#{@cmdman_marker}") {
		if m != "" {
			t.Errorf("after detach, pane still carries a marker: %q", m)
		}
	}
	if got := tmuxWindowOption(t, socket, wid, "pane-border-status"); got == "top" {
		t.Errorf("pane-border-status still %q after detach; want it cleared", got)
	}

	// The disposable-viewer guarantee: commands keep running, untouched.
	for _, name := range names {
		if info := env.inspectJSON(ctx, name); info["State"] != "running" {
			t.Errorf("after detach %s state = %v, want running", name, info["State"])
		}
		if got := env.livePID(ctx, name); got != pids[name] {
			t.Errorf("after detach %s pid changed %v -> %v (restarted, not left alone)",
				name, pids[name], got)
		}
	}
}

// composeMuxCustomSocketYAML is like composeMuxYAML but includes a custom
// driver_opt.socket, used by tests that verify driver_opt passthrough.
func composeMuxCustomSocketYAML(project, socket string) string {
	return composeMuxYAML(project, socket)
}

// TestMuxLs_HonorsDriverOpt verifies that `cmdman mux ls <specPath>` passes
// the spec's driver_opt (including a custom socket) to mux.List, so dashboards
// on a non-default tmux server are visible. This covers the fix recorded in the
// 2026-06-12 decision log: `mux ls` gained an optional [path] argument with the
// same semantics as `mux down [path]` (read only for driver/driver_opt).
func TestMuxLs_HonorsDriverOpt(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })

	env.run(ctx, "run", "-n", "solo", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, "solo") })
	env.waitForState(ctx, "solo", "running", defaultTimeout)

	// The spec declares driver_opt.socket so both `mux up` and `mux ls <path>`
	// target the same isolated server.
	specPath := writeSpecFile(t, singleMuxYAML(socket))

	if _, stderr, err := env.muxExec(ctx, "mux", specPath); err != nil {
		t.Fatalf("cmdman mux failed: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowID(t, socket, "cmdman")
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 1 {
		t.Fatalf("want 1 pane after up, got %d", got)
	}

	// `mux ls <specPath>` must find the window on the custom socket by reading
	// driver_opt from the spec, not by querying the default server.
	stdout, stderr, err := env.muxExec(
		ctx, "mux", "ls", "--format", "{{.SessionName}}\t{{.Identity}}", specPath,
	)
	if err != nil {
		t.Fatalf("cmdman mux ls failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	const wantSession = "cmdman"
	const wantIdentity = "cmdman"
	found := false
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && parts[0] == wantSession && parts[1] == wantIdentity {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf(
			"mux ls with spec path: expected row with session=%q identity=%q; got:\n%s",
			wantSession, wantIdentity, stdout,
		)
	}
}

// TestComposeMuxLs_HonorsDriverOpt verifies that `compose mux ls` passes the
// spec's driver_opt (including a custom socket) to mux.List, so dashboards on a
// non-default tmux server are visible. This covers the fix recorded in the
// 2026-06-12 decision log: `runComposeMuxLs` now passes spec.Driver and
// spec.DriverOpt to mux.List, the same way `runComposeMuxDown` does.
func TestComposeMuxLs_HonorsDriverOpt(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "muxlssock"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(t, wd, composeMuxCustomSocketYAML(project, socket))
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
		t.Fatalf("compose mux failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 2 {
		t.Fatalf("want 2 panes after up, got %d", got)
	}

	// `compose mux ls` must find the window on the custom socket by reading
	// driver_opt from the compose spec. Before the fix this would silently query
	// the default server and return no rows.
	stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath,
		"mux", "ls", "--format", "{{.SessionName}}\t{{.Identity}}",
	)
	if err != nil {
		t.Fatalf(
			"compose mux ls failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout, stderr,
		)
	}

	const wantSession = "cmdman"
	identitySuffix := "-" + project
	found := false
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 &&
			parts[0] == wantSession &&
			strings.HasSuffix(parts[1], identitySuffix) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf(
			"compose mux ls with custom socket: expected row with session=%q "+
				"and identity suffix %q; got:\n%s",
			wantSession, identitySuffix, stdout,
		)
	}
}

// TestComposeMux_DetachRestoresWindow verifies `cmdman compose mux --detach`
// restores the project-named window (cmdman-<project>) to a single clean pane
// while the backing services keep running.
func TestComposeMux_DetachRestoresWindow(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	wd := composeWorkdir(t)
	project := "muxdetach"
	socket := muxSocket(t)
	t.Cleanup(func() { killTmuxServer(t, socket) })
	composePath := writeComposeFile(t, wd, composeMuxYAML(project, socket))
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
		t.Fatalf("compose mux failed: %v\nstderr:\n%s", err, stderr)
	}
	window := "cmdman-" + project
	wid := tmuxWindowID(t, socket, window)
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 2 {
		t.Fatalf("want 2 panes before detach, got %d", got)
	}

	if downStdout, downStderr, downErr := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "down",
	); downErr != nil {
		t.Fatalf(
			"compose mux down failed: %v\nstdout:\n%s\nstderr:\n%s",
			downErr, downStdout, downStderr,
		)
	} else if !strings.Contains(downStdout, "Restored window") {
		t.Fatalf("expected 'Restored window ...' on stdout; got:\n%s", downStdout)
	}

	if got := tmuxWindowID(t, socket, window); got != wid {
		t.Fatalf("window id changed across detach: %s vs %s", wid, got)
	}
	// Count by pane_id, not pane_title: detach clears the restored pane's title.
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_id}")); got != 1 {
		t.Fatalf("want 1 pane after detach, got %d", got)
	}
	if got := tmuxWindowOption(t, socket, wid, "pane-border-status"); got == "top" {
		t.Errorf("pane-border-status still %q after detach; want it cleared", got)
	}
	// The backing services keep running (compose commands are addressed by their
	// generated ID, found via the project labels — service names are not direct
	// cmdman names).
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		id := e["ID"].(string)
		if info := env.inspectJSON(ctx, id); info["State"] != "running" {
			t.Errorf("after detach %s state = %v, want running", id, info["State"])
		}
	}
}

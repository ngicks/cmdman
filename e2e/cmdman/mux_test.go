package cmdman_test

import (
	"bytes"
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

	"github.com/ngicks/cmdman/pkg/cmdman"
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

// muxExec runs the cmdman binary like testEnv.exec, but with $TMUX and $ZELLIJ
// stripped from the environment so the mux driver deterministically takes the
// "outside a multiplexer" path (build detached + print the attach hint),
// regardless of whether the test process itself is running inside tmux. The
// target server is still the dedicated driver_opt.socket from the spec.
func (e *testEnv) muxExec(ctx context.Context, args ...string) (string, string, error) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdmanBin, args...)
	base := slices.DeleteFunc(os.Environ(), func(s string) bool {
		return strings.HasPrefix(s, "TMUX=") || strings.HasPrefix(s, "ZELLIJ=")
	})
	base = append(base,
		cmdman.ENV_CMDMAN_DATA_DIR+"="+e.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+e.runtimeDir,
		cmdman.ENV_CMDMAN_CONF+"="+e.confPath,
	)
	cmd.Env = base
	cmd.WaitDelay = 3 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// standaloneMuxYAML is a single-layout spec with three side-by-side panes
// bound to commands api/worker/cache, on the given dedicated tmux socket.
func standaloneMuxYAML(socket string) string {
	return fmt.Sprintf(`mux:
  driver: tmux
  driver_opt:
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
		env.waitForState(ctx, name, "started", defaultTimeout)
		ids[name] = env.inspectJSON(ctx, name)["id"].(string)
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

	// One pane per command, titled by command name (first run ⇒ marker 0).
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
	env.waitForState(ctx, "mouseapp", "started", defaultTimeout)

	specPath := writeSpecFile(t, fmt.Sprintf(`mux:
  driver: tmux
  driver_opt:
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
  driver: tmux
  driver_opt:
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
		env.waitForState(ctx, name, "started", defaultTimeout)
	}

	specPath := writeSpecFile(t, cycleMuxYAML(socket))

	// First run → layout index 0 ("wide"): two panes, marker 0.
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

	// Second run → layout index 1 ("solo"): single pane, marker 1.
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
  driver: tmux
  driver_opt:
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

	env.run(ctx, "run", "-n", "solo", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, "solo") })
	env.waitForState(ctx, "solo", "started", defaultTimeout)
	pidBefore := env.livePID(ctx, "solo")

	specPath := writeSpecFile(t, singleMuxYAML(socket))
	if _, stderr, err := env.muxExec(ctx, "mux", specPath); err != nil {
		t.Fatalf("cmdman mux failed: %v\nstderr:\n%s", err, stderr)
	}
	wid := tmuxWindowID(t, socket, "cmdman")
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_title}")); got != 1 {
		t.Fatalf("want 1 pane before kill, got %d", got)
	}

	// Tear the viewer down entirely.
	killTmuxServer(t, socket)
	// Let any SIGHUP-driven teardown that would (wrongly) reach the command
	// have time to land before we assert it did not.
	time.Sleep(500 * time.Millisecond)

	info := env.inspectJSON(ctx, "solo")
	if info["state"] != "started" {
		t.Fatalf(
			"killing the mux session changed command state to %v; mux must be view-only",
			info["state"],
		)
	}
	if pidAfter := env.livePID(ctx, "solo"); pidAfter != pidBefore {
		t.Fatalf(
			"command pid changed across mux teardown (was %v, now %v): restarted, not left alone",
			pidBefore,
			pidAfter,
		)
	}
}

// livePID returns the live OS pid of a running command from `cmdman inspect`.
func (e *testEnv) livePID(ctx context.Context, idOrName string) float64 {
	e.t.Helper()
	info := e.inspectJSON(ctx, idOrName)
	live, _ := info["live_status"].(map[string]any)
	if live == nil {
		e.t.Fatalf("no live_status for %q; command not running?\n%v", idOrName, info)
	}
	pid, _ := live["pid"].(float64)
	if pid <= 0 {
		e.t.Fatalf("bad live pid for %q: %v", idOrName, live["pid"])
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
  driver: tmux
  driver_opt:
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
		env.waitForState(ctx, e["ID"].(string), "started", defaultTimeout)
	}

	stdout, stderr, err := env.muxExec(ctx, "compose", "--workdir", wd, "-f", composePath, "mux")
	if err != nil {
		t.Fatalf("compose mux failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "tmux attach -t cmdman") {
		t.Fatalf("expected attach hint on stdout; got:\n%s", stdout)
	}

	// compose mux names the owned window cmdman-<project>.
	wid := tmuxWindowID(t, socket, "cmdman-"+project)
	if got, want := windowPaneBases(
		t,
		socket,
		wid,
	), []string{
		"alpha",
		"beta",
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
		env.waitForState(ctx, name, "started", defaultTimeout)
		pids[name] = env.livePID(ctx, name)
	}

	specPath := writeSpecFile(t, standaloneMuxYAML(socket))

	// Build the dashboard: three panes, pane-border-status enabled.
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

	// Detach: restore the window.
	if stdout, stderr, err := env.muxExec(ctx, "mux", "--detach", specPath); err != nil {
		t.Fatalf("cmdman mux --detach failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// The owned window survives (restored, not killed) ...
	if got := tmuxWindowID(t, socket, "cmdman"); got != wid {
		t.Fatalf("window id changed across detach: %s vs %s", wid, got)
	}
	// ... collapsed to a single pane ...
	// Count by pane_id, not pane_title: detach clears the restored pane's title.
	if got := len(tmuxPaneField(t, socket, wid, "#{pane_id}")); got != 1 {
		t.Fatalf("want 1 pane after detach, got %d", got)
	}
	// ... with cmdman's tmux options cleared.
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
		if info := env.inspectJSON(ctx, name); info["state"] != "started" {
			t.Errorf("after detach %s state = %v, want started", name, info["state"])
		}
		if got := env.livePID(ctx, name); got != pids[name] {
			t.Errorf("after detach %s pid changed %v -> %v (restarted, not left alone)",
				name, pids[name], got)
		}
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
		env.waitForState(ctx, e["ID"].(string), "started", defaultTimeout)
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

	if stdout, stderr, err := env.muxExec(
		ctx, "compose", "--workdir", wd, "-f", composePath, "mux", "--detach",
	); err != nil {
		t.Fatalf("compose mux --detach failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
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
		if info := env.inspectJSON(ctx, id); info["state"] != "started" {
			t.Errorf("after detach %s state = %v, want started", id, info["state"])
		}
	}
}

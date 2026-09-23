package cmdman_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stopWrapperScript stands in for a wrapper shell whose own child carries a
// stop out, such as a script that runs podman. It starts the child in the
// background and waits on it, so its trap runs the moment the stop's signal
// lands rather than once the child is done. The trap holds on only until the
// child reports ready, then exits: the wrapper is reaped while whatever the
// child started in response to the stop is still running.
//
// An asynchronous command of a shell without job control starts with SIGINT
// ignored, and a non-interactive bash cannot trap a signal it started with
// ignored. The subshell restores SIGINT before it execs the child, so a child
// can trap INT the way it traps TERM.
const stopWrapperScript = `
trap 'while [ ! -e "$READY_FILE" ]; do sleep 0.05; done; exit 0' TERM INT
(trap - INT; exec "$BASH" -c "$CHILD_SCRIPT") &
wait
`

// stopHelperChildScript simulates a process that carries a stop out through a
// helper of its own, the way podman forks crun to kill its container. On
// $STOP_SIGNAL it forks the helper, reports ready to the wrapper, and waits for
// the helper. The marker is written only when that wait completes with the
// helper's success, so a signal that reaches the helper or interrupts the
// child leaves no marker.
//
// The stop's signal is trapped only once: a second delivery is appended to the
// record file instead of starting over with a fresh helper, which would write
// the marker all the same. A TERM that is not the stop's signal is recorded
// too. HUP is ignored ahead of everything, and the helper inherits that: on a
// tty the wrapper leads the session, and its exit hangs up the terminal's
// whole foreground group. The idle sleep ignores SIGINT like any asynchronous
// command, so the child ends it itself. The pid file is written last, so a
// test that has read it knows every trap is in place.
const stopHelperChildScript = `
trap "" HUP
sleep 300 &
idle=$!
on_stop() {
	trap 'echo "$STOP_SIGNAL" >> "$RECORD_FILE"' "$STOP_SIGNAL"
	sleep "$HELPER_SECONDS" &
	helper=$!
	touch "$READY_FILE"
	wait "$helper" && touch "$MARKER_FILE"
	kill "$idle" 2>/dev/null
	exit 0
}
trap 'echo TERM >> "$RECORD_FILE"; exit 1' TERM
trap on_stop "$STOP_SIGNAL"
echo $$ > "$CHILD_PID_FILE"
wait
`

// stopStubbornChildScript simulates a process that does not stop on the stop's
// signal: it ignores TERM and HUP, and its foreground sleep inherits both. It
// reports ready at once, so the wrapper exits on the stop's signal and leaves
// the child behind for the stop's SIGKILL.
const stopStubbornChildScript = `
trap "" TERM HUP
echo $$ > "$CHILD_PID_FILE"
touch "$READY_FILE"
sleep 300
`

// stopOwnGroupChildScript simulates a process that moved part of itself into a
// process group of its own inside the command's session, where a signal to the
// command's process group does not reach. Job control gives the background
// sleep that group. The sleep keeps TERM ignored, while the child itself goes
// back to the default and dies of the stop's signal.
const stopOwnGroupChildScript = `
set -m
trap "" TERM
sleep 300 &
echo $! > "$SURVIVOR_PID_FILE"
trap - TERM
touch "$READY_FILE"
wait
`

// requireBash returns the bash the stop wrapper scripts run under. They rely
// on bash's trap and wait semantics, which a POSIX sh does not promise.
func requireBash(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found in PATH; the stop wrapper tests need its trap and wait semantics")
	}
	return path
}

// wrapperRun is a command running child under stopWrapperScript, with the
// files the scripts and the test hand off through.
type wrapperRun struct {
	name        string
	ready       string
	marker      string
	record      string
	childPid    string
	survivorPid string
}

// startWrapper runs child under stopWrapperScript as the command name, waits
// for it to reach running, and registers its removal. extraEnv reaches both
// scripts.
func startWrapper(
	t *testing.T,
	ctx context.Context,
	env *testEnv,
	name string,
	tty bool,
	child string,
	extraEnv ...string,
) *wrapperRun {
	t.Helper()
	bash := requireBash(t)
	dir := t.TempDir()
	w := &wrapperRun{
		name:        name,
		ready:       filepath.Join(dir, "ready"),
		marker:      filepath.Join(dir, "marker"),
		record:      filepath.Join(dir, "record"),
		childPid:    filepath.Join(dir, "child.pid"),
		survivorPid: filepath.Join(dir, "survivor.pid"),
	}

	args := []string{"run", "-n", name}
	if tty {
		args = append(args, "-t")
	}
	for _, kv := range slices.Concat([]string{
		"READY_FILE=" + w.ready,
		"MARKER_FILE=" + w.marker,
		"RECORD_FILE=" + w.record,
		"CHILD_PID_FILE=" + w.childPid,
		"SURVIVOR_PID_FILE=" + w.survivorPid,
		"CHILD_SCRIPT=" + child,
	}, extraEnv) {
		args = append(args, "-E", kv)
	}
	args = append(args, "--", bash, "-c", stopWrapperScript)

	env.run(ctx, args...)
	// Not ctx: cleanup runs after the test's context is already cancelled.
	t.Cleanup(func() { env.cleanupCommand(context.Background(), name) })
	env.waitForState(ctx, name, "running", defaultTimeout)
	return w
}

// recordedEvent is the part of an event log entry the stop wrapper tests read.
type recordedEvent struct {
	Time  time.Time         `json:"time"`
	Attrs map[string]string `json:"attrs"`
	raw   string
}

// lastEvent returns the command's last recorded event of type typ.
func lastEvent(
	t *testing.T,
	ctx context.Context,
	env *testEnv,
	name, typ string,
) recordedEvent {
	t.Helper()
	id := env.resolvedID(ctx, name)
	lines := splitNonEmptyLines(
		env.run(ctx, "events", "--no-follow", "--id", id, "--type", typ),
	)
	if len(lines) == 0 {
		t.Fatalf("no %s event recorded for %q", typ, name)
	}
	ev := recordedEvent{raw: lines[len(lines)-1]}
	if err := json.Unmarshal([]byte(ev.raw), &ev); err != nil {
		t.Fatalf("decode %s event %q: %v", typ, ev.raw, err)
	}
	return ev
}

// assertNoSurvivorsUnreaped fails the test when the command's last exited
// event reports processes the run gave up on.
func assertNoSurvivorsUnreaped(t *testing.T, ctx context.Context, env *testEnv, name string) {
	t.Helper()
	ev := lastEvent(t, ctx, env, name, "exited")
	if n, ok := ev.Attrs["survivors_unreaped"]; ok {
		t.Errorf("exited event reports survivors_unreaped=%s: %s", n, ev.raw)
	}
}

// TestStop_WrapperHelper pins a stop of a command whose wrapper exits on the
// stop's signal while its child still carries the stop out through a helper.
// The monitor sends nothing of its own while a stop is in progress, so the
// helper runs to completion, the child writes its marker, and the run ends
// with nothing left for the monitor to give up on. The patient variant keeps
// the helper running for most of the stop's timeout, and the stop waits it out.
func TestStop_WrapperHelper(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		helper     string
		stopArgs   []string
		minElapsed time.Duration
	}{
		{name: "race", helper: "0.5"},
		{name: "patience", helper: "3", stopArgs: []string{"-t", "5"}, minElapsed: 3 * time.Second},
	} {
		for _, tty := range []bool{false, true} {
			mode := "pipe"
			if tty {
				mode = "tty"
			}
			t.Run(tc.name+"_"+mode, func(t *testing.T) {
				t.Parallel()
				ctx := testContext(t)
				env := newTestEnv(t)

				w := startWrapper(t, ctx, env, "wrapped-helper", tty, stopHelperChildScript,
					"STOP_SIGNAL=TERM", "HELPER_SECONDS="+tc.helper)
				child := readPidFile(t, w.childPid)
				t.Cleanup(func() { killIfAlive(child) })

				start := time.Now()
				env.run(ctx, slices.Concat([]string{"stop"}, tc.stopArgs, []string{w.name})...)
				elapsed := time.Since(start)

				env.waitForState(ctx, w.name, "exited", defaultTimeout)
				if !fileExists(w.marker) {
					t.Errorf("no marker: the helper the child forked to carry out the stop " +
						"did not run to completion undisturbed")
				}
				if rec := strings.TrimSpace(readFile(t, w.record)); rec != "" {
					t.Errorf("the child received signals after the stop's own: %q", rec)
				}
				assertNoSurvivorsUnreaped(t, ctx, env, w.name)
				if elapsed < tc.minElapsed {
					t.Errorf(
						"stop took %s, less than the %s the helper needed",
						elapsed,
						tc.minElapsed,
					)
				}
			})
		}
	}
}

// TestStop_WrapperEscalation pins the stop's SIGKILL as what ends a leftover
// that ignores the stop's signal: the wrapper is gone at once, the monitor
// waits instead of signalling, and the child goes down only at the timeout.
func TestStop_WrapperEscalation(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	w := startWrapper(t, ctx, env, "wrapped-stubborn", false, stopStubbornChildScript)
	child := readPidFile(t, w.childPid)
	t.Cleanup(func() { killIfAlive(child) })

	start := time.Now()
	env.run(ctx, "stop", "-t", "1", w.name)
	elapsed := time.Since(start)

	env.waitForState(ctx, w.name, "exited", defaultTimeout)
	waitUntil(t, 5*time.Second, func() bool { return !processExists(child) },
		"child pid %d is still in /proc after the stop's SIGKILL", child)
	if elapsed < time.Second {
		t.Errorf("stop took %s, less than its 1s timeout; something other than "+
			"the stop's SIGKILL ended the child", elapsed)
	}
	assertNoSurvivorsUnreaped(t, ctx, env, w.name)
}

// TestStop_WrapperConfiguredSignal pins a stop with a signal other than TERM:
// the child carries the stop out on INT, and nothing sends it a TERM on top.
func TestStop_WrapperConfiguredSignal(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	w := startWrapper(t, ctx, env, "wrapped-int", false, stopHelperChildScript,
		"STOP_SIGNAL=INT", "HELPER_SECONDS=0.5")
	child := readPidFile(t, w.childPid)
	t.Cleanup(func() { killIfAlive(child) })

	env.run(ctx, "stop", "-s", "INT", w.name)

	env.waitForState(ctx, w.name, "exited", defaultTimeout)
	if !fileExists(w.marker) {
		t.Errorf("no marker: the helper the child forked on INT did not run to completion " +
			"undisturbed")
	}
	if rec := strings.TrimSpace(readFile(t, w.record)); rec != "" {
		t.Errorf("the child received signals besides the stop's INT: %q", rec)
	}
	assertNoSurvivorsUnreaped(t, ctx, env, w.name)
}

// TestStop_WrapperOwnProcessGroup pins the stop's SIGKILL reaching a leftover
// in a process group of its own, which the SIGKILL to the command's process
// group misses: the monitor completes it by pid.
func TestStop_WrapperOwnProcessGroup(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	w := startWrapper(t, ctx, env, "wrapped-own-group", false, stopOwnGroupChildScript)
	survivor := readPidFile(t, w.survivorPid)
	t.Cleanup(func() { killIfAlive(survivor) })

	start := time.Now()
	env.run(ctx, "stop", "-t", "1", w.name)
	elapsed := time.Since(start)

	env.waitForState(ctx, w.name, "exited", defaultTimeout)
	waitUntil(t, 5*time.Second, func() bool { return !processExists(survivor) },
		"survivor pid %d in its own process group is still in /proc", survivor)
	if elapsed < time.Second {
		t.Errorf("stop took %s, less than its 1s timeout; something other than "+
			"the stop's SIGKILL ended the survivor", elapsed)
	}
	assertNoSurvivorsUnreaped(t, ctx, env, w.name)
}

// TestStop_WrapperAbandonedClient pins the monitor's own escalation: a client
// interrupted before its timeout never sends the SIGKILL, and the deadline the
// monitor armed for the stop sends it instead.
func TestStop_WrapperAbandonedClient(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	w := startWrapper(t, ctx, env, "wrapped-abandoned", false, stopStubbornChildScript)
	child := readPidFile(t, w.childPid)
	t.Cleanup(func() { killIfAlive(child) })
	wrapper := int(env.livePID(ctx, w.name))

	client := env.Cmd("stop", "-t", "1", w.name).build(ctx)
	var out bytes.Buffer
	client.Stdout = &out
	client.Stderr = &out
	start := time.Now()
	must(t, client.Start())
	t.Cleanup(func() {
		_ = client.Process.Kill()
		_ = client.Wait()
	})

	// The child reported ready at start, so the wrapper exits the moment the
	// stop's signal reaches it. The monitor arms its deadline before it sends
	// that signal, so a wrapper that is gone means the deadline is armed.
	for processExists(wrapper) {
		if time.Since(start) > defaultTimeout {
			t.Fatalf("wrapper pid %d never went away; the stop's signal did not reach it", wrapper)
		}
		time.Sleep(10 * time.Millisecond)
	}
	must(t, client.Process.Signal(syscall.SIGINT))
	interrupted := time.Now()
	err := client.Wait()
	t.Logf("interrupted stop: %v\n%s", err, out.String())

	env.waitForState(ctx, w.name, "exited", 5*time.Second)
	waitUntil(t, 5*time.Second, func() bool { return !processExists(child) },
		"child pid %d is still in /proc; the monitor's deadline did not kill it", child)
	assertNoSurvivorsUnreaped(t, ctx, env, w.name)

	// The client records the stop just before it sends the stop request, and
	// its own timeout starts only once that request returns. An interrupt
	// within a second of the record therefore landed before the client could
	// have sent its SIGKILL, so the SIGKILL that ended the child was the
	// monitor's.
	requested := lastEvent(t, ctx, env, w.name, "stopped").Time
	if gap := interrupted.Sub(requested); gap >= time.Second {
		t.Fatalf("the interrupt went out %s after the client recorded the stop, too late "+
			"to rule out the client's own SIGKILL", gap)
	}
}

package monitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/config"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// hookDispatcherFor returns a started dispatcher whose hook runs are recorded
// by execFn instead of being spawned.
func hookDispatcherFor(
	t *testing.T,
	set model.HookSet,
	execFn hookExecFunc,
) *hookDispatcher {
	t.Helper()
	d := newHookDispatcher(nil)
	d.execFn = execFn
	d.configure(model.HookLayers{Command: set}, t.TempDir(), []string{"PATH=/bin"})
	d.start(t.Context())
	t.Cleanup(d.close)
	return d
}

func TestHookDispatcher_RunsConfiguredHook(t *testing.T) {
	type run struct {
		argv []string
		env  []string
	}
	runs := make(chan run, 4)
	d := hookDispatcherFor(t,
		model.HookSet{
			model.HookEventNotification: {Exec: []string{"notify", "--now"}},
		},
		func(_ context.Context, argv []string, _ string, env []string) error {
			runs <- run{argv: argv, env: env}
			return nil
		},
	)

	d.dispatch(hookEvent{
		Kind:  model.HookEventNotification,
		Title: "Agent",
		Body:  "needs input",
	})

	got := receiveHookRun(t, runs)
	assert.DeepEqual(t, got.argv, []string{"notify", "--now"})
	assert.Equal(t, envValue(got.env, config.ENV_CMDMAN_HOOK_EVENT), "notification")
	assert.Equal(t, envValue(got.env, config.ENV_CMDMAN_HOOK_TITLE), "Agent")
	assert.Equal(t, envValue(got.env, config.ENV_CMDMAN_HOOK_BODY), "needs input")
	// The command's own environment comes along, so a hook can reach back into
	// cmdman with the CMDMAN_CMD_ID family already set.
	assert.Equal(t, envValue(got.env, "PATH"), "/bin")
}

func TestHookDispatcher_IgnoresEventsWithoutExec(t *testing.T) {
	runs := make(chan struct{}, 1)
	d := hookDispatcherFor(t,
		// Blocking is a byte-stream action; it runs nothing.
		model.HookSet{model.HookEventBell: {Action: model.HookActionBlock}},
		func(context.Context, []string, string, []string) error {
			runs <- struct{}{}
			return nil
		},
	)

	d.dispatch(hookEvent{Kind: model.HookEventBell})
	d.dispatch(hookEvent{Kind: model.HookEventTitle, Title: "t"})

	select {
	case <-runs:
		t.Fatal("hook ran for an event with no exec configured")
	case <-time.After(100 * time.Millisecond):
	}
	assert.Equal(t, d.dropped.Load(), uint64(0))
}

func TestHookDispatcher_SerializesRuns(t *testing.T) {
	var (
		concurrent atomic.Int32
		maxSeen    atomic.Int32
		done       = make(chan struct{}, 8)
	)
	d := hookDispatcherFor(t,
		model.HookSet{model.HookEventBell: {Exec: []string{"hook"}}},
		func(context.Context, []string, string, []string) error {
			if n := concurrent.Add(1); n > maxSeen.Load() {
				maxSeen.Store(n)
			}
			time.Sleep(20 * time.Millisecond)
			concurrent.Add(-1)
			done <- struct{}{}
			return nil
		},
	)

	// Dispatch slowly enough that nothing is dropped, so every event is
	// observed and the runs can only be serial.
	for range 4 {
		d.dispatch(hookEvent{Kind: model.HookEventBell})
		time.Sleep(40 * time.Millisecond)
	}
	for range 4 {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for hook runs")
		}
	}
	assert.Equal(t, maxSeen.Load(), int32(1))
}

func TestHookDispatcher_DropsOnFlood(t *testing.T) {
	var ran atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	d := hookDispatcherFor(t,
		model.HookSet{model.HookEventBell: {Exec: []string{"hook"}}},
		func(context.Context, []string, string, []string) error {
			ran.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return nil
		},
	)

	d.dispatch(hookEvent{Kind: model.HookEventBell})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first hook never started")
	}

	// One event fits the queue behind the running hook; the rest are dropped
	// rather than made to wait, because dispatch runs on the latch path.
	for range 20 {
		d.dispatch(hookEvent{Kind: model.HookEventBell})
	}
	assert.Assert(t, d.dropped.Load() >= 19, "dropped=%d", d.dropped.Load())
	assert.Equal(t, ran.Load(), int32(1))

	close(release)
}

// A restart reconfigures the dispatcher while an event may still be queued
// behind a running hook. That event belongs to the run that captured it, so it
// must run with that run's argv, directory and environment - not the next
// run's.
func TestHookDispatcher_QueuedEventKeepsItsOwnRunsConfig(t *testing.T) {
	type run struct {
		argv []string
		dir  string
		env  []string
	}
	var (
		runs    = make(chan run, 4)
		release = make(chan struct{})
		started = make(chan struct{}, 1)
	)
	firstDir := t.TempDir()
	d := newHookDispatcher(nil)
	d.execFn = func(_ context.Context, argv []string, dir string, env []string) error {
		runs <- run{argv: argv, dir: dir, env: env}
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	}
	d.configure(
		model.HookLayers{
			Command: model.HookSet{model.HookEventBell: {Exec: []string{"first"}}},
		},
		firstDir,
		[]string{"RUN=1"},
	)
	d.start(t.Context())
	t.Cleanup(d.close)

	// The first event occupies the worker; the second waits in the queue.
	d.dispatch(hookEvent{Kind: model.HookEventBell})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first hook never started")
	}
	d.dispatch(hookEvent{Kind: model.HookEventBell})

	d.configure(
		model.HookLayers{
			Command: model.HookSet{model.HookEventBell: {Exec: []string{"second"}}},
		},
		t.TempDir(),
		[]string{"RUN=2"},
	)
	close(release)

	for range 2 {
		got := receiveHookRun(t, runs)
		assert.DeepEqual(t, got.argv, []string{"first"})
		assert.Equal(t, got.dir, firstDir)
		assert.Equal(t, envValue(got.env, "RUN"), "1")
	}
}

func TestHookDispatcher_ExecPassesEventEnvToTheProcess(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "hook.out")
	d := newHookDispatcher(nil)
	d.configure(
		model.HookLayers{
			Global: model.HookSet{model.HookEventTitle: {Exec: []string{
				"sh",
				"-c",
				`printf '%s|%s|%s' "$CMDMAN_HOOK_EVENT" "$CMDMAN_HOOK_TITLE" "$CMDMAN_CMD_ID" > "$0"`,
				out,
			}}},
		},
		dir,
		[]string{"PATH=" + os.Getenv("PATH"), config.ENV_CMDMAN_CMD_ID + "=cmd-1"},
	)
	d.start(t.Context())
	t.Cleanup(d.close)

	d.dispatch(hookEvent{Kind: model.HookEventTitle, Title: "building"})

	waitUntil(t, 5*time.Second, func() bool {
		_, err := os.Stat(out)
		return err == nil
	}, "hook never wrote %q", out)

	data, err := os.ReadFile(out)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "title|building|cmd-1")
}

func TestHookDispatcher_CloseStopsARunningHook(t *testing.T) {
	d := newHookDispatcher(nil)
	d.configure(
		model.HookLayers{
			Global: model.HookSet{model.HookEventBell: {Exec: []string{"sleep", "60"}}},
		},
		t.TempDir(),
		[]string{"PATH=" + os.Getenv("PATH")},
	)
	d.start(t.Context())

	d.dispatch(hookEvent{Kind: model.HookEventBell})
	// Give the worker a moment to actually be inside the sleep.
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	d.close()
	assert.Assert(t, time.Since(start) < hookWaitDelay+2*time.Second,
		"close took %s", time.Since(start))
}

func TestCommandRuntimeState_DispatchesCapturedSequences(t *testing.T) {
	st, feed := runtimeStateFeed(t)
	var got []hookEvent
	st.setHookSink(func(ev hookEvent) { got = append(got, ev) })

	feed("\a\a")
	feed("\x1b]2;building\x07")
	// A shell that retitles with the same string is not a new event.
	feed("\x1b]2;building\x07")
	feed("\x1b]777;notify;Agent;needs input\x07")
	// Opening an attach reads the bell (D11); one that rings afterwards latches
	// again, and the hook fires per bell throughout.
	st.attachBegin()
	feed("\a")

	assert.DeepEqual(t, got, []hookEvent{
		{Kind: model.HookEventBell},
		{Kind: model.HookEventBell},
		{Kind: model.HookEventTitle, Title: "building"},
		{Kind: model.HookEventNotification, Title: "Agent", Body: "needs input"},
		{Kind: model.HookEventBell},
	})
	assert.Equal(t, st.snapshot().BellUnread, true)
}

func receiveHookRun[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a hook run")
	}
	var zero T
	return zero
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok && name == key {
			return value
		}
	}
	return ""
}

// waitUntil polls fn until it returns true or the timeout expires.
func waitUntil(t *testing.T, timeout time.Duration, fn func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf(format, args...)
}

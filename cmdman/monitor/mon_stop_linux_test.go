//go:build linux

package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"golang.org/x/sys/unix"
	"gotest.tools/v3/assert"
)

// termIgnoringSurvivorScript leaves a process behind in the command's process
// group that ignores SIGTERM, and writes its pid to $1. An ignored disposition
// survives the exec into sleep, so only SIGKILL ends the leftover.
const termIgnoringSurvivorScript = `trap "" TERM
sleep 300 </dev/null >/dev/null 2>&1 & echo $! >"$1"`

// A stop has already signalled the command's whole process group once, and
// what outlives the command is most likely carrying that stop out. The run end
// leaves it be - no signal of its own, however long it takes - until the
// stop's own SIGKILL takes it.
func TestMonitorStopAwaitSendsNoSignal(t *testing.T) {
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	survivorPidPath := filepath.Join(dir, "survivor.pid")
	t.Cleanup(func() { killSurvivor(t, survivorPidPath) })

	m := newShellMonitor(
		t, dir, "test-monitor-stop-await-no-signal",
		termIgnoringSurvivorScript, survivorPidPath,
	)

	// The terminating sweep and the await share the recorder, so a signal from
	// either shows up.
	rec := &killRecorder{deliver: true}
	m.sweepFn = func(
		ctx context.Context,
		logger *slog.Logger,
		pgid int,
		stopRequested func() bool,
	) int {
		opts := defaultSweepOptions()
		opts.kill = rec.kill
		opts.stopRequested = stopRequested
		return sweepRunSurvivorsWith(ctx, logger, pgid, opts)
	}
	awaiting := make(chan struct{})
	m.awaitFn = func(
		ctx context.Context,
		logger *slog.Logger,
		pgid int,
		killed func() bool,
	) int {
		close(awaiting)
		opts := defaultSweepOptions()
		opts.kill = rec.kill
		return awaitRunSurvivorsWith(ctx, logger, pgid, killed, opts)
	}

	// The script exits the moment its leftover is started, so the stop is
	// latched ahead of the run: what matters is that one is in progress when
	// the run ends. Without a timeout it arms no deadline of its own.
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 0))

	done := startRun(t, m)
	awaitClosed(t, awaiting, "the run never reached the await")

	survivor, ok := readPidFile(t, survivorPidPath)
	assert.Assert(t, ok, "the command never reported the pid it left behind")

	// The terminating sweep would have sent SIGTERM at once and SIGKILL after
	// its grace; this is long enough for the first to show.
	select {
	case got := <-done:
		t.Fatalf(
			"the run ended (code %d, err %v) while the stop was in progress",
			got.code,
			got.err,
		)
	case <-time.After(500 * time.Millisecond):
	}
	stat, found := procStatOf(t, survivor)
	assert.Assert(t, found && stat.state != "Z", "the leftover did not live through the await")
	assert.Assert(
		t,
		len(rec.calls()) == 0,
		"the run end signalled while a stop was in progress: %v",
		rec.calls(),
	)

	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))
	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)

	assert.Assert(t, survivorGone(t, survivor), "the stop's SIGKILL did not end the leftover")
	// The leftover sat in the command's process group, so the stop's own
	// SIGKILL took it and the run end had nothing to add.
	assert.Assert(t, len(rec.calls()) == 0, "the run end signalled: %v", rec.calls())
	assert.Assert(t, len(m.runAnomalies) == 0, "the run reported %v", m.runAnomalies)
}

// A stop's SIGKILL goes to the command's process group, and a survivor leading
// a group of its own inside the command's session is out of its reach. The run
// end finishes that SIGKILL by the survivor's own pid, and by that pid only: a
// member of the group has the stop's SIGKILL already.
func TestMonitorStopKillReachesSurvivorInItsOwnGroup(t *testing.T) {
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	var (
		inGroupPidPath  = filepath.Join(dir, "in-group.pid")
		ownGroupPidPath = filepath.Join(dir, "own-group.pid")
	)
	t.Cleanup(func() {
		killSurvivor(t, inGroupPidPath)
		killSurvivor(t, ownGroupPidPath)
	})

	m, _, _ := newSurvivorMonitor(t, dir, "test-monitor-stop-kill-own-group", []string{
		sweepHelperOrphanEnv + "=" + inGroupPidPath,
		sweepHelperOwnGroupEnv + "=" + ownGroupPidPath,
	})

	rec := &killRecorder{deliver: true}
	awaiting := make(chan struct{})
	m.awaitFn = func(
		ctx context.Context,
		logger *slog.Logger,
		pgid int,
		killed func() bool,
	) int {
		close(awaiting)
		opts := defaultSweepOptions()
		opts.kill = rec.kill
		return awaitRunSurvivorsWith(ctx, logger, pgid, killed, opts)
	}

	// The helper exits the moment its leftovers are started, so the stop is
	// latched ahead of the run.
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 0))
	done := startRun(t, m)
	awaitClosed(t, awaiting, "the run never reached the await")

	inGroup, ok := readPidFile(t, inGroupPidPath)
	assert.Assert(t, ok, "the helper in the command's group never reported a pid")
	ownGroup, ok := readPidFile(t, ownGroupPidPath)
	assert.Assert(t, ok, "the helper in a group of its own never reported a pid")
	// Without these the kill below could reach both through the command's group
	// and prove nothing about the await.
	inStat, found := procStatOf(t, inGroup)
	assert.Assert(t, found && inStat.state != "Z", "the leftover in the group was gone early")
	assert.Equal(t, inStat.pgrp, inStat.session, "the leftover left the command's group")
	ownStat, found := procStatOf(t, ownGroup)
	assert.Assert(t, found && ownStat.state != "Z", "the leftover in its own group was gone early")
	assert.Equal(t, ownStat.pgrp, ownGroup, "the leftover does not lead a group of its own")
	assert.Equal(t, ownStat.session, inStat.session, "the leftover left the command's session")

	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))
	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)

	calls := rec.calls()
	assert.Assert(t, len(calls) > 0, "the run end never killed the leftover in its own group")
	for _, c := range calls {
		assert.Equal(
			t,
			c,
			killCall{pid: ownGroup, sig: syscall.SIGKILL},
			"the run end sent something other than SIGKILL to the leftover in its own group: %v",
			calls,
		)
	}
	assert.Assert(t, survivorGone(t, inGroup), "the stop's SIGKILL did not end its group")
	assert.Assert(t, survivorGone(t, ownGroup), "the leftover in its own group outlived the run")
	assert.Assert(t, len(m.runAnomalies) == 0, "the run reported %v", m.runAnomalies)
}

// Completing a stop's SIGKILL adds a SIGKILL by pid for a survivor outside the
// command's process group and for nothing else: a member of the group has the
// stop's own SIGKILL already. Here nothing is delivered, so every survivor stays
// alive through every rescan the bound allows, and each rescan must pick the
// same one.
func TestAwaitAfterStopKillSignalsOnlyOutsideTheGroup(t *testing.T) {
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	var (
		inGroupPidPath  = filepath.Join(dir, "in-group.pid")
		ownGroupPidPath = filepath.Join(dir, "own-group.pid")
	)
	t.Cleanup(func() {
		killSurvivor(t, inGroupPidPath)
		killSurvivor(t, ownGroupPidPath)
	})

	// The helper leads its own session the way a run's command does, and its
	// leftovers are reparented here once it exits.
	exe, err := os.Executable()
	assert.NilError(t, err)
	helper := exec.CommandContext(t.Context(), exe, "-test.run=^TestSweepHelperProcess$")
	helper.Env = append(
		testEnv(),
		sweepHelperOrphanEnv+"="+inGroupPidPath,
		sweepHelperOwnGroupEnv+"="+ownGroupPidPath,
	)
	prepCommandAttrs(helper)
	assert.NilError(t, helper.Run())

	ownGroup, ok := readPidFile(t, ownGroupPidPath)
	assert.Assert(t, ok, "the helper in a group of its own never reported a pid")
	_, ok = readPidFile(t, inGroupPidPath)
	assert.Assert(t, ok, "the helper in the command's group never reported a pid")

	rec := &killRecorder{}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	alive := awaitRunSurvivorsWith(
		t.Context(),
		logger,
		helper.Process.Pid,
		func() bool { return true },
		sweepOptions{grace: 50 * time.Millisecond, bound: 300 * time.Millisecond, kill: rec.kill},
	)

	assert.Equal(t, alive, 2, "the await did not report both leftovers as still alive")
	calls := rec.calls()
	// The bound allows several rescans, which is what shows the choice holds on
	// each one and not only on the first.
	assert.Assert(t, len(calls) > 1, "the await rescanned no more than once: %v", calls)
	for _, c := range calls {
		assert.Equal(
			t,
			c,
			killCall{pid: ownGroup, sig: syscall.SIGKILL},
			"the await signalled something other than the leftover outside the group: %v",
			calls,
		)
	}
}

// A survivor that the stop's SIGKILL cannot take down - one wedged in an
// uninterruptible wait ignores it too - holds the run for the await's bound
// only, and the run reports it.
func TestMonitorRunReportsSurvivorsTheStopKillCouldNotReap(t *testing.T) {
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	ownGroupPidPath := filepath.Join(dir, "own-group.pid")
	t.Cleanup(func() { killSurvivor(t, ownGroupPidPath) })

	id := "test-monitor-stop-kill-bound"
	m, appCfg, st := newSurvivorMonitor(t, dir, id, []string{
		sweepHelperOwnGroupEnv + "=" + ownGroupPidPath,
	})

	// A kill that delivers nothing stands in for a process that will not die.
	// The bound here is the test's; the monitor's is ten seconds.
	const bound = 500 * time.Millisecond
	awaiting := make(chan struct{})
	m.awaitFn = func(
		ctx context.Context,
		logger *slog.Logger,
		pgid int,
		killed func() bool,
	) int {
		close(awaiting)
		return awaitRunSurvivorsWith(ctx, logger, pgid, killed, sweepOptions{
			grace: 50 * time.Millisecond,
			bound: bound,
			kill:  func(int, syscall.Signal) error { return nil },
		})
	}

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 0))
	done := startRun(t, m)
	awaitClosed(t, awaiting, "the run never reached the await")

	killedAt := time.Now()
	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))
	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)
	assert.Assert(
		t,
		time.Since(killedAt) >= bound,
		"the run gave up on the leftover ahead of the bound",
	)

	// runLoop is what publishes the outcome of a run; do what it does so the
	// anomaly can be read back where a user would find it.
	m.setExited(0)

	eventPath, err := appCfg.EventLogPath()
	assert.NilError(t, err)
	exited := lastEventOfType(t, eventPath, model.EventTypeExited)
	unreaped, err := strconv.Atoi(exited.Attrs["survivors_unreaped"])
	assert.NilError(t, err, "the exited event carries no survivor count: %v", exited.Attrs)
	assert.Assert(t, unreaped >= 1, "the run reported %d survivors", unreaped)

	_, _, stateJSON, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.DeepEqual(t, stateJSON.Warnings, []string{
		fmt.Sprintf("%d survivor(s) still alive after sweep bound", unreaped),
	})
}

// A monitor shutting down cannot see a stop through, and what the command left
// behind would outlive its supervisor. The await gives up at once on the
// cancelled run, and the terminating sweep takes over on a bound of its own,
// told that no stop is in progress so it does not hand the leftover back.
func TestMonitorShutdownDuringStopFallsBackToSweep(t *testing.T) {
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	survivorPidPath := filepath.Join(dir, "survivor.pid")
	t.Cleanup(func() { killSurvivor(t, survivorPidPath) })

	// A command that leads its own session and leaves a process in it, the way
	// a run does. The leftover is reparented here once the shell exits.
	cmd := exec.CommandContext(
		t.Context(),
		"/bin/sh", "-c", `sleep 300 </dev/null >/dev/null 2>&1 & echo $! >"$1"`,
		"sh", survivorPidPath,
	)
	prepCommandAttrs(cmd)
	assert.NilError(t, cmd.Run())
	survivor, ok := readPidFile(t, survivorPidPath)
	assert.Assert(t, ok, "the command never reported the pid it left behind")
	stat, found := procStatOf(t, survivor)
	assert.Assert(t, found && stat.state != "Z", "the leftover was gone before the run end")

	var m Monitor
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 0))

	// What the sweep was handed is read inside it: sweepSurvivors cancels the
	// sweep's context on its way out.
	type sweepCall struct {
		hasDeadline bool
		err         error
		stopping    bool
	}
	var calls []sweepCall
	m.sweepFn = func(
		ctx context.Context,
		logger *slog.Logger,
		pgid int,
		stopRequested func() bool,
	) int {
		_, hasDeadline := ctx.Deadline()
		calls = append(calls, sweepCall{
			hasDeadline: hasDeadline,
			err:         ctx.Err(),
			stopping:    stopRequested(),
		})
		return sweepRunSurvivors(ctx, logger, pgid, stopRequested)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m.sweepSurvivors(ctx, cmd.Process.Pid)

	assert.Equal(t, len(calls), 1, "the terminating sweep ran %d times", len(calls))
	assert.Assert(t, calls[0].hasDeadline, "the sweep that took over has no bound")
	assert.NilError(t, calls[0].err, "the sweep that took over inherited the shutdown")
	assert.Assert(t, !calls[0].stopping, "the sweep that took over was told a stop is going")
	assert.Assert(t, survivorGone(t, survivor), "the leftover outlived the shutdown")
	assert.Assert(t, len(m.runAnomalies) == 0, "the run reported %v", m.runAnomalies)
}

// Once the stop's SIGKILL is out, the await runs its kill phase on a bound of
// its own, and a monitor shutting down meanwhile adds no terminating sweep on
// top. The run reports what the kill phase left alive.
func TestMonitorShutdownAfterStopKillKeepsAwaitCount(t *testing.T) {
	var m Monitor
	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))

	swept := 0
	m.sweepFn = func(context.Context, *slog.Logger, int, func() bool) int {
		swept++
		return 0
	}
	const unreaped = 3
	var sawKill bool
	m.awaitFn = func(_ context.Context, _ *slog.Logger, _ int, killed func() bool) int {
		sawKill = killed()
		return unreaped
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// The stubs never look at the group, so any positive pgid does.
	m.sweepSurvivors(ctx, 1)

	assert.Assert(t, sawKill, "the await was not told the stop's SIGKILL is out")
	assert.Equal(t, swept, 0, "the terminating sweep ran %d times after the kill phase", swept)
	assert.Assert(
		t,
		slices.Equal(m.runAnomalies, []runAnomaly{anomalySurvivorsUnreaped(unreaped)}),
		"the run reported %v",
		m.runAnomalies,
	)
}

// A stop can land while the terminating sweep is going. From then on what is
// left belongs to the stop: the sweep sends nothing further, does not sit out
// the rest of its grace, and the await takes over until the stop's own SIGKILL.
func TestMonitorStopDuringSweepHandsOver(t *testing.T) {
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	survivorPidPath := filepath.Join(dir, "survivor.pid")
	t.Cleanup(func() { killSurvivor(t, survivorPidPath) })

	m := newShellMonitor(
		t, dir, "test-monitor-stop-during-sweep",
		termIgnoringSurvivorScript, survivorPidPath,
	)

	// The stop lands inside the first round, as soon as the sweep has sent its
	// first signal, and from a goroutine of its own the way a Stop RPC does, so
	// when it lands does not hang on when the sweep looks for it. stoppedAt and
	// stopErr are read once stopLanded is closed.
	stopLanded := make(chan struct{})
	var (
		stoppedAt time.Time
		stopErr   error
	)
	landStop := sync.OnceFunc(func() {
		go func() {
			defer close(stopLanded)
			stoppedAt = time.Now()
			stopErr = m.StopProcess(syscall.SIGTERM, 0)
		}()
	})
	sweepRec := &killRecorder{deliver: true}
	m.sweepFn = func(
		ctx context.Context,
		logger *slog.Logger,
		pgid int,
		stopRequested func() bool,
	) int {
		opts := defaultSweepOptions()
		// A grace far past the sweep's bound: a sweep that did not wake when the
		// stop landed would hold the run for the whole bound.
		opts.grace = time.Minute
		opts.kill = func(pid int, sig syscall.Signal) error {
			err := sweepRec.kill(pid, sig)
			landStop()
			return err
		}
		opts.stopRequested = stopRequested
		return sweepRunSurvivorsWith(ctx, logger, pgid, opts)
	}
	awaitRec := &killRecorder{deliver: true}
	awaiting := make(chan struct{})
	var awaitAt time.Time
	m.awaitFn = func(
		ctx context.Context,
		logger *slog.Logger,
		pgid int,
		killed func() bool,
	) int {
		awaitAt = time.Now()
		close(awaiting)
		opts := defaultSweepOptions()
		opts.kill = awaitRec.kill
		return awaitRunSurvivorsWith(ctx, logger, pgid, killed, opts)
	}

	done := startRun(t, m)
	awaitClosed(t, stopLanded, "the sweep never signalled the leftover")
	awaitClosed(t, awaiting, "the sweep never handed the leftover over to the stop")

	assert.NilError(t, stopErr)
	assert.Assert(
		t,
		awaitAt.Sub(stoppedAt) < orphanGrace,
		"the sweep sat out its grace for %s before handing over",
		awaitAt.Sub(stoppedAt),
	)

	survivor, ok := readPidFile(t, survivorPidPath)
	assert.Assert(t, ok, "the command never reported the pid it left behind")
	select {
	case got := <-done:
		t.Fatalf(
			"the run ended (code %d, err %v) while the stop was in progress",
			got.code,
			got.err,
		)
	case <-time.After(300 * time.Millisecond):
	}
	stat, found := procStatOf(t, survivor)
	assert.Assert(t, found && stat.state != "Z", "the leftover did not live through the await")

	assert.NilError(t, m.StopProcess(syscall.SIGKILL, 0))
	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)

	// The SIGTERM is what set the stop off; anything after it came once the stop
	// had landed.
	assert.Assert(
		t,
		slices.Equal(sweepRec.calls(), []killCall{{pid: survivor, sig: syscall.SIGTERM}}),
		"the sweep signalled after the stop landed: %v",
		sweepRec.calls(),
	)
	assert.Assert(t, len(awaitRec.calls()) == 0, "the await signalled: %v", awaitRec.calls())
	assert.Assert(t, survivorGone(t, survivor), "the stop's SIGKILL did not end the leftover")
	assert.Assert(t, len(m.runAnomalies) == 0, "the run reported %v", m.runAnomalies)
}

// killCall is one signal a sweep or an await sent.
type killCall struct {
	pid int
	sig syscall.Signal
}

// killRecorder stands in for the kill a sweep or an await sends. It records
// every call and delivers the signal only when deliver is set. The run records
// on its own goroutine while the test reads on its, hence the lock.
type killRecorder struct {
	deliver bool

	mu    sync.Mutex
	kills []killCall
}

func (r *killRecorder) kill(pid int, sig syscall.Signal) error {
	r.mu.Lock()
	r.kills = append(r.kills, killCall{pid: pid, sig: sig})
	r.mu.Unlock()
	if !r.deliver {
		return nil
	}
	return unix.Kill(pid, sig)
}

func (r *killRecorder) calls() []killCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.kills)
}

// awaitClosed waits for ch to be closed and fails the test with msg when that
// takes longer than any run in these tests should.
func awaitClosed(t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		t.Fatal(msg)
	}
}

// A run is not over when its child is reaped: the survivor sweep and the
// output-reader drain still have to finish, and a stop escalating to SIGKILL
// routinely lands in that window. The run keeps the command's process group id
// for exactly that long, so the escalation reaches what the command left behind
// instead of being refused for want of a live child.
func TestMonitorSignalReachesGroupDuringSweep(t *testing.T) {
	// Only what is reparented here can be waited on below, which is how the
	// signal's effect is told apart from a pid that simply went away.
	becomeSubreaperForTest(t)

	dir := t.TempDir()
	survivorPidPath := filepath.Join(dir, "survivor.pid")
	t.Cleanup(func() { killSurvivor(t, survivorPidPath) })

	m, _, _ := newSurvivorMonitor(t, dir, "test-monitor-signal-during-sweep", []string{
		sweepHelperOrphanEnv + "=" + survivorPidPath,
	})

	// Holding the sweep open is what makes the window a test can act in. The
	// real sweep would take the leftover away by itself and prove nothing about
	// the signal, and it would reap it too, which is why the wait below is the
	// test's own.
	sweeping := make(chan struct{})
	release := make(chan struct{})
	m.sweepFn = func(context.Context, *slog.Logger, int, func() bool) int {
		close(sweeping)
		<-release
		return 0
	}

	// The run's error comes back to the test goroutine: a failed assertion calls
	// FailNow, which only the test goroutine may do.
	runErr := make(chan error, 1)
	go func() {
		_, err := m.runOnce(t.Context())
		runErr <- err
	}()

	select {
	case <-sweeping:
	case <-time.After(30 * time.Second):
		t.Fatal("the run never reached its sweep")
	}
	// Registered once the sweep is known to be held, so a failed assertion below
	// still lets the run finish instead of leaving it parked for good.
	releaseSweep := sync.OnceFunc(func() { close(release) })
	defer releaseSweep()

	// Without this the assertions below would pass on the live-child path and
	// say nothing about a run that has already given its handles up.
	_, _, pid := m.GetState()
	assert.Equal(t, pid, 0, "the run still reports a live process")

	survivor, ok := readPidFile(t, survivorPidPath)
	assert.Assert(t, ok, "the command never reported the pid it left behind")
	stat, found := procStatOf(t, survivor)
	assert.Assert(t, found && stat.state != "Z", "the leftover was gone before the signal")

	assert.NilError(t, m.SignalProcess(syscall.SIGKILL))
	assert.Assert(
		t,
		len(waitGone(t.Context(), []int{survivor}, 10*time.Second)) == 0,
		"the signal never reached the process the command left behind",
	)

	releaseSweep()
	select {
	case err := <-runErr:
		assert.NilError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the run never ended")
	}

	// With the run over there is no group left to aim at, so the next caller is
	// told so rather than signalling a pid that has been handed out again.
	assert.ErrorIs(t, m.SignalProcess(syscall.SIGKILL), errNoRunningProcess)
}

// A stop with a timeout escalates on the monitor's own clock. The client that
// asked for it may never come back to send SIGKILL, so a command ignoring the
// stop signal is killed once the timeout expires by the one stop alone.
func TestMonitorStopDeadlineKillsCommandIgnoringSignal(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(
		t, dir, "test-monitor-stop-deadline",
		`trap "" TERM; : >"$1"; sleep 300`, readyPath,
	)

	done := startRun(t, m)
	awaitCommandReady(t, m, readyPath)

	const timeout = 300 * time.Millisecond
	stoppedAt := time.Now()
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, timeout))

	// The run's context stays live and the test never signals the command
	// again, so the deadline is the only SIGKILL there is.
	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1, "the command was not killed")
	assert.Assert(
		t,
		time.Since(stoppedAt) >= timeout,
		"the command ended ahead of the deadline, so the stop signal took it",
	)
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
}

// The deadline reaches what the command left behind as well. A stop landing
// while the run winds down finds no live child, and a leftover that ignores
// the stop signal is taken by the deadline through the group the run keeps.
func TestMonitorStopDeadlineReachesWhatTheCommandLeftBehind(t *testing.T) {
	// The leftover is reparented here once the shell exits, which is what lets
	// the wait below reap it.
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	survivorPidPath := filepath.Join(dir, "survivor.pid")
	t.Cleanup(func() { killSurvivor(t, survivorPidPath) })

	m := newShellMonitor(
		t, dir, "test-monitor-stop-deadline-survivor",
		`trap "" TERM; sleep 300 </dev/null >/dev/null 2>&1 & echo $! >"$1"`, survivorPidPath,
	)

	// The real sweep would take the leftover away by itself, so it is held open
	// for the deadline to be what acts on it.
	sweeping := make(chan struct{})
	release := make(chan struct{})
	m.sweepFn = func(context.Context, *slog.Logger, int, func() bool) int {
		close(sweeping)
		<-release
		return 0
	}

	done := startRun(t, m)
	select {
	case <-sweeping:
	case <-time.After(30 * time.Second):
		t.Fatal("the run never reached its sweep")
	}
	releaseSweep := sync.OnceFunc(func() { close(release) })
	defer releaseSweep()

	_, _, pid := m.GetState()
	assert.Equal(t, pid, 0, "the run still reports a live process")

	survivor, ok := readPidFile(t, survivorPidPath)
	assert.Assert(t, ok, "the command never reported the pid it left behind")
	stat, found := procStatOf(t, survivor)
	assert.Assert(t, found && stat.state != "Z", "the leftover was gone before the stop")

	const timeout = 300 * time.Millisecond
	stoppedAt := time.Now()
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, timeout))
	assert.Assert(
		t,
		len(waitGone(t.Context(), []int{survivor}, 10*time.Second)) == 0,
		"the deadline never reached the process the command left behind",
	)
	assert.Assert(
		t,
		time.Since(stoppedAt) >= timeout,
		"the leftover went ahead of the deadline, so the stop signal took it",
	)

	releaseSweep()
	assert.Equal(t, awaitRun(t, done, 30*time.Second), 0)
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
}

// A stop without a timeout leaves escalation to the client. The monitor must
// not make one up: a command ignoring the stop signal keeps running.
func TestMonitorStopWithoutTimeoutNeverEscalates(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(
		t, dir, "test-monitor-stop-no-deadline",
		`trap "" TERM; : >"$1"; sleep 300`, readyPath,
	)

	done := startRun(t, m)
	pid := awaitCommandReady(t, m, readyPath)

	assert.NilError(t, m.StopProcess(syscall.SIGTERM, 0))
	assert.Assert(t, armedStopDeadline(m) == nil, "a stop without a timeout armed a deadline")

	// Several times the deadline the tests above use.
	select {
	case got := <-done:
		t.Fatalf(
			"the run ended (code %d, err %v) with nothing escalating the stop",
			got.code,
			got.err,
		)
	case <-time.After(1500 * time.Millisecond):
	}
	_, _, livePid := m.GetState()
	assert.Equal(t, livePid, pid, "the command is no longer running")

	assert.NilError(t, m.SignalProcess(syscall.SIGKILL))
	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
}

// A deadline must not outlive its run: past the run's end the group it would
// aim at can be handed out again. The run stops it where it gives the group up.
func TestMonitorRunEndDisarmsStopDeadline(t *testing.T) {
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	m := newShellMonitor(
		t,
		dir,
		"test-monitor-stop-deadline-disarm",
		`: >"$1"; sleep 300`,
		readyPath,
	)

	done := startRun(t, m)
	awaitCommandReady(t, m, readyPath)

	// The stop signal takes this command at once, and the deadline is far enough
	// out that only the run's end can have stopped it.
	assert.NilError(t, m.StopProcess(syscall.SIGTERM, time.Minute))
	armed := armedStopDeadline(m)
	assert.Assert(t, armed != nil, "the stop armed no deadline")

	assert.Equal(t, awaitRun(t, done, 10*time.Second), -1)
	assert.Assert(t, armedStopDeadline(m) == nil, "the run ended with the deadline still armed")
	// Stop reports whether it was the one to stop the timer. A minute has not
	// passed, so false means the run stopped it rather than merely dropping it.
	assert.Assert(t, !armed.Stop(), "the run let go of the deadline without stopping it")
}

// newShellMonitor supervises script under /bin/sh in place of the helper
// process newSurvivorMonitor wires: the helper's leftovers take SIGTERM's
// default action, and a shell's trap is what hands a process that ignores it.
// runOnce runs the command m.cfg names, so swapping the argv in memory is
// enough for it. runLoop re-reads the config from disk and would run the helper
// instead. args become the script's positional parameters.
func newShellMonitor(t *testing.T, dir, id, script string, args ...string) *Monitor {
	t.Helper()
	m, _, _ := newSurvivorMonitor(t, dir, id, nil)
	m.cfg.Argv = append([]string{"/bin/sh", "-c", script, "sh"}, args...)
	return m
}

type runOutcome struct {
	code int
	err  error
}

// startRun runs the command once on a goroutine of its own so the test can act
// on the run while it goes. A test that fails before the run ends would leave
// a command that ignores SIGTERM running, so cleanup kills whatever is left.
func startRun(t *testing.T, m *Monitor) <-chan runOutcome {
	t.Helper()
	done := make(chan runOutcome, 1)
	go func() {
		code, err := m.runOnce(t.Context())
		done <- runOutcome{code: code, err: err}
	}()
	t.Cleanup(func() { _ = m.SignalProcess(syscall.SIGKILL) })
	return done
}

// awaitRun returns the exit code of the run startRun began. It runs on the test
// goroutine because a failed assertion calls FailNow, which only the test
// goroutine may do.
func awaitRun(t *testing.T, done <-chan runOutcome, timeout time.Duration) int {
	t.Helper()
	select {
	case got := <-done:
		assert.NilError(t, got.err)
		return got.code
	case <-time.After(timeout):
		t.Fatal("the run never ended")
		return -1
	}
}

// awaitCommandReady waits until the run has published its child and the
// script has written readyPath, and returns the child's pid. The scripts write
// it once their trap is in place: a stop landing before that would take the
// shell outright and say nothing about the deadline.
func awaitCommandReady(t *testing.T, m *Monitor, readyPath string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, _, pid := m.GetState()
		if _, err := os.Stat(readyPath); err == nil && pid != 0 {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never became ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func armedStopDeadline(m *Monitor) *time.Timer {
	m.procMu.Lock()
	defer m.procMu.Unlock()
	return m.stopDeadline
}

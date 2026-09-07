//go:build linux

package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"gotest.tools/v3/assert"
)

// The environment tells the helper process which survivors to start and where
// to report their pids.
const (
	sweepHelperDetachedEnv = "CMDMAN_TEST_SWEEP_DETACHED_PID"
	sweepHelperOrphanEnv   = "CMDMAN_TEST_SWEEP_ORPHAN_PID"
)

// A command can leave processes behind: one that broke away into a session of
// its own, and one that simply outlived the process that started it. The
// monitor is the reaper of both, so the run terminates them before it finishes
// instead of handing them to init, where they would go on holding whatever they
// hold and stack up across restarts.
func TestMonitorRunSweepsWhatTheCommandLeftBehind(t *testing.T) {
	// The sweep only reaches what is reparented to this process. In the monitor
	// RunMonitor arranges that; here the test process is the monitor.
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	var (
		detachedPidPath = filepath.Join(dir, "detached.pid")
		orphanPidPath   = filepath.Join(dir, "orphan.pid")
	)
	t.Cleanup(func() {
		killSurvivor(t, detachedPidPath)
		killSurvivor(t, orphanPidPath)
	})

	m, _, _ := newSurvivorMonitor(t, dir, "test-monitor-sweep-survivors", []string{
		sweepHelperDetachedEnv + "=" + detachedPidPath,
		sweepHelperOrphanEnv + "=" + orphanPidPath,
	})

	// A hook keeps the monitor's session, so the sweep must leave it alone even
	// though it is a child of the monitor just like the survivors are.
	hook := exec.CommandContext(t.Context(), "sleep", "30")
	prepHookAttrs(hook)
	assert.NilError(t, hook.Start())
	t.Cleanup(func() {
		_ = hook.Process.Kill()
		_ = hook.Wait()
	})

	assert.Equal(t, runOnceWithin(t, m, 30*time.Second), 0)

	detached, ok := readPidFile(t, detachedPidPath)
	assert.Assert(t, ok, "the helper in a session of its own never reported a pid")
	orphan, ok := readPidFile(t, orphanPidPath)
	assert.Assert(t, ok, "the helper in the command's session never reported a pid")

	// The one that broke away is out of reach of any group-wide signal, so only
	// a scan for what the monitor has been made the parent of finds it.
	assert.Assert(
		t,
		survivorGone(t, detached),
		"the process that broke away into its own session outlived the run",
	)
	assert.Assert(
		t,
		survivorGone(t, orphan),
		"the process the command left behind outlived the run",
	)

	hookStat, hookFound := procStatOf(t, hook.Process.Pid)
	assert.Assert(
		t,
		hookFound && hookStat.state != "Z",
		"the sweep took the running hook down with the command's leftovers",
	)

	assert.Assert(t, len(m.runAnomalies) == 0, "the run reported %v", m.runAnomalies)
}

// A process wedged in an uninterruptible wait ignores SIGKILL as well, so the
// sweep gives up at its bound instead of holding the run open on it, and the
// run reports how many it left behind.
func TestMonitorRunReportsSurvivorsTheSweepCouldNotReap(t *testing.T) {
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	survivorPidPath := filepath.Join(dir, "survivor.pid")
	t.Cleanup(func() { killSurvivor(t, survivorPidPath) })

	id := "test-monitor-sweep-bound"
	m, appCfg, st := newSurvivorMonitor(t, dir, id, []string{
		sweepHelperOrphanEnv + "=" + survivorPidPath,
	})

	// Signals that deliver nothing stand in for a process that will not die, so
	// the sweep runs its whole course and gives up on something still alive.
	// The bound here is the test's; the monitor's is ten seconds.
	m.sweepFn = func(ctx context.Context, logger *slog.Logger, pgid int) int {
		ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		return sweepRunSurvivorsWith(ctx, logger, pgid, sweepOptions{
			grace: 50 * time.Millisecond,
			kill:  func(int, syscall.Signal) error { return nil },
		})
	}

	assert.Equal(t, runOnceWithin(t, m, 30*time.Second), 0)

	// runLoop is what publishes the outcome of a run; do what it does so the
	// anomaly can be read back where a user would find it.
	m.setExited(0)

	eventPath, err := appCfg.EventLogPath()
	assert.NilError(t, err)
	exited := lastEventOfType(t, eventPath, model.EventTypeExited)
	// The count is whatever the sweep found, not only this test's helper:
	// anything else outside the monitor's session is a leftover of the run too.
	unreaped, err := strconv.Atoi(exited.Attrs["survivors_unreaped"])
	assert.NilError(t, err, "the exited event carries no survivor count: %v", exited.Attrs)
	assert.Assert(t, unreaped >= 1, "the sweep reported %d survivors", unreaped)

	_, _, stateJSON, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.DeepEqual(t, stateJSON.Warnings, []string{
		fmt.Sprintf("%d survivor(s) still alive after sweep bound", unreaped),
	})
}

// TestSweepHelperProcess is not a test of its own: it is the command the sweep
// tests supervise. It starts the processes the environment asks for, reports
// their pids and exits without waiting for either, which is what leaves them
// for the monitor to deal with. Starting them from here rather than from a
// shell keeps the tests off setsid(1), which is not everywhere.
func TestSweepHelperProcess(t *testing.T) {
	var (
		detachedPidPath = os.Getenv(sweepHelperDetachedEnv)
		orphanPidPath   = os.Getenv(sweepHelperOrphanEnv)
	)
	if detachedPidPath == "" && orphanPidPath == "" {
		t.Skip("helper process: runs only as the command of a sweep test")
	}
	if detachedPidPath != "" {
		startSurvivor(t, detachedPidPath, true)
	}
	if orphanPidPath != "" {
		startSurvivor(t, orphanPidPath, false)
	}
}

// startSurvivor starts a process that outlives this one and writes down the pid
// it runs under. ownSession puts it in a session of its own, the way a program
// that deliberately detaches itself does. Its output goes to the null device,
// so what these tests observe is the sweep alone and not the run's drain of the
// output a leftover holds open.
func startSurvivor(t *testing.T, pidPath string, ownSession bool) {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if ownSession {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	assert.NilError(t, cmd.Start())
	assert.NilError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600))
}

// newSurvivorMonitor wires the helper process above as one non-TTY command, the
// way a monitor does for a run, and returns the monitor that supervises it. env
// is what tells the helper which survivors to start.
func newSurvivorMonitor(
	t *testing.T,
	dir, id string,
	env []string,
) (*Monitor, config.Config, *store.Store) {
	t.Helper()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	t.Cleanup(func() { st.Close() })

	// os.Args[0] can be relative to the directory the tests were started in,
	// and the command runs elsewhere.
	exe, err := os.Executable()
	assert.NilError(t, err)

	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{exe, "-test.run=^TestSweepHelperProcess$"},
		Dir:             dir,
		Env:             append(testEnv(), env...),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}
	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m, err := newMonitor(t.Context(), id, appCfg, logger)
	assert.NilError(t, err)
	t.Cleanup(func() { m.Close() })
	return m, appCfg, st
}

// runOnceWithin runs the command once and returns its exit code. The run drives
// a goroutine of its own because a failed assertion calls FailNow, which only
// the test goroutine may do.
func runOnceWithin(t *testing.T, m *Monitor, timeout time.Duration) int {
	t.Helper()
	type outcome struct {
		code int
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		code, err := m.runOnce(t.Context())
		done <- outcome{code: code, err: err}
	}()
	select {
	case got := <-done:
		assert.NilError(t, got.err)
		return got.code
	case <-time.After(timeout):
		t.Fatal("the run never ended")
		return -1
	}
}

// readPidFile reads the pid a survivor wrote down for itself, reporting
// ok=false when it never got that far.
func readPidFile(t *testing.T, path string) (int, bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// killSurvivor ends the process named by the pid file and waits on it. The test
// process is the subreaper, so a survivor that is never waited on stays a
// zombie child for the rest of the package's tests.
func killSurvivor(t *testing.T, pidPath string) {
	t.Helper()
	killPidFile(t, pidPath)
	pid, ok := readPidFile(t, pidPath)
	if !ok {
		return
	}
	_ = waitGone(context.Background(), []int{pid}, 2*time.Second)
}

// survivorGone reports that pid is neither running nor waiting to be reaped.
func survivorGone(t *testing.T, pid int) bool {
	t.Helper()
	st, found := procStatOf(t, pid)
	if !found {
		return true
	}
	// A pid is handed out again once it has been reaped, so only an entry still
	// parented here speaks for the survivor.
	return st.ppid != os.Getpid()
}

// procStatOf reads pid's stat fields, reporting found=false when the process is
// gone.
func procStatOf(t *testing.T, pid int) (procStat, bool) {
	t.Helper()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, false
	}
	return parseProcStat(t, string(b)), true
}

//go:build linux

package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/logdriver/k8sfile"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"gotest.tools/v3/assert"
)

// A non-TTY command can leave behind a process that inherited its stdout and
// then holds the pipe open for far longer than the command itself lived. The
// run reads that pipe itself, so the child being reaped is what ends it: the
// sweep takes the leftover down, the read ends on its own, and the exit code
// the command really returned is what gets reported. Leaving the pipe to
// os/exec instead made cmd.Wait join a copying goroutine the leftover kept
// alive, so a run like this one took the whole exec delay and was then reported
// failed even though its command had exited zero.
func TestMonitorPipeRunEndsWhileALeftoverHoldsStdout(t *testing.T) {
	// The sweep is what takes the leftover down, and it reaches only what is
	// reparented to this process. In the monitor RunMonitor arranges that; here
	// the test process is the monitor.
	assert.NilError(t, becomeSubreaper())

	dir := t.TempDir()
	holderPidPath := filepath.Join(dir, "holder.pid")
	t.Cleanup(func() { killSurvivor(t, holderPidPath) })

	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	// The helper inherits the command's stdout and outlives it by minutes, so
	// nothing but the sweep ever lets go of the run's pipe.
	script := fmt.Sprintf(
		`sleep 300 &
echo $! > '%s'
echo first-run
exit 0
`,
		holderPidPath,
	)

	id := "test-monitor-pipe-leftover-holds-stdout"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", script},
		Dir:             dir,
		Env:             testEnv(),
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
	defer m.Close()

	started := time.Now()
	assert.Equal(t, runOnceWithin(t, m, 30*time.Second), 0)
	elapsed := time.Since(started)
	// The command exits at once and the sweep ends the leftover it left holding
	// the pipe, so the run is over in well under the delay exec would have
	// spent waiting on that pipe.
	assert.Assert(t, elapsed < 5*time.Second, "the run took %s to end", elapsed)
	assert.Assert(t, len(m.runAnomalies) == 0, "the run reported %v", m.runAnomalies)

	holder, ok := readPidFile(t, holderPidPath)
	assert.Assert(t, ok, "the command never reported the pid of what it left behind")
	assert.Assert(
		t,
		survivorGone(t, holder),
		"the process holding the run's stdout outlived the run",
	)

	scrollback := string(m.ring.Bytes())
	assert.Assert(t, strings.Contains(scrollback, "first-run"), "scrollback: %q", scrollback)

	consoleLog, err := os.ReadFile(filepath.Join(commandDir, k8sfile.DefaultLogFileName))
	assert.NilError(t, err)
	assert.Assert(
		t,
		strings.Contains(string(consoleLog), "first-run"),
		"log: %q",
		string(consoleLog),
	)
}

// When nothing takes the leftover away - a process in an uninterruptible wait,
// or a monitor that never became a subreaper - the read on the run's stdout
// stays parked. The run gives it the drain wait, reports what it gave up on and
// ends anyway, and closing the read end is what frees the reader afterwards.
func TestMonitorPipeRunEndsWhenTheOutputReaderStaysBlocked(t *testing.T) {
	dir := t.TempDir()
	holderPidPath := filepath.Join(dir, "holder.pid")
	t.Cleanup(func() { killSurvivor(t, holderPidPath) })

	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	script := fmt.Sprintf(
		`sleep 300 &
echo $! > '%s'
echo first-run
exit 0
`,
		holderPidPath,
	)

	id := "test-monitor-pipe-reader-detach"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", script},
		Dir:             dir,
		Env:             testEnv(),
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
	defer m.Close()

	// What is under test is the run's behaviour while the pipe is still held, so
	// nothing may take the holder away: the sweep would end it and the read
	// would finish on its own.
	m.sweepFn = func(context.Context, *slog.Logger, int) int { return 0 }

	started := time.Now()
	assert.Equal(t, runOnceWithin(t, m, 30*time.Second), 0)
	elapsed := time.Since(started)
	// The command exits at once and the holder lives for five minutes, so the
	// run's length is the drain wait and nothing else.
	assert.Assert(
		t,
		elapsed >= readerDrainWait,
		"the run ended in %s, without waiting for its reader at all",
		elapsed,
	)
	assert.Assert(t, elapsed < 10*time.Second, "the run took %s to end", elapsed)

	// runLoop is what publishes the outcome of a run; do what it does so the
	// anomaly can be read back where a user would find it.
	m.setExited(0)

	_, _, stateJSON, err := st.GetCommandState(id)
	assert.NilError(t, err)
	assert.DeepEqual(t, stateJSON.Warnings, []string{"output reader still blocked after 1s"})

	eventPath, err := appCfg.EventLogPath()
	assert.NilError(t, err)
	exited := lastEventOfType(t, eventPath, model.EventTypeExited)
	assert.Equal(t, exited.Attrs["reader_detached"], "true")

	// The output the command produced before the wedge still made it through.
	scrollback := string(m.ring.Bytes())
	assert.Assert(t, strings.Contains(scrollback, "first-run"), "scrollback: %q", scrollback)
}

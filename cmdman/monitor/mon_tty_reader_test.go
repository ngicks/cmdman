package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/logdriver/k8sfile"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"gotest.tools/v3/assert"
)

// staleOutputMarker is what the process that escaped the first command writes
// to the PTY of the run it outlived. Nothing else writes it, so finding it in
// the second run's output means the reader the first run left behind delivered
// output on behalf of a run that had already ended.
const staleOutputMarker = "stale-from-detached-reader"

// A TTY command can leave a process behind that holds the PTY slave open. The
// read parked on the master then never ends - closing the master does not wake
// it - so a run that joined its reader would report a command whose own process
// is long gone as running forever. The run gives the reader a moment for its
// trailing output, then leaves it behind and drops whatever it reads after.
func TestMonitorTtyRunEndsWithReaderStillBlocked(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	var (
		holderPidPath = filepath.Join(dir, "holder.pid")
		triggerPath   = filepath.Join(dir, "trigger")
		wrotePath     = filepath.Join(dir, "wrote")
	)
	t.Cleanup(func() { killPidFile(t, holderPidPath) })

	// The helper outlives the command that spawned it, ignores the signals a
	// stop would send it, and holds the PTY slave for far longer than the test
	// runs. It writes to that PTY only once the test asks it to, which is how
	// the write lands after the run the PTY belongs to has ended.
	firstScript := fmt.Sprintf(
		`trap "" TERM HUP
(until [ -e '%s' ]; do sleep 0.05; done; echo %s; : > '%s'; exec sleep 300) &
echo $! > '%s'
trap - TERM HUP
echo first-run
exit 0
`,
		triggerPath, staleOutputMarker, wrotePath, holderPidPath,
	)

	id := "test-monitor-tty-reader-detach"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", firstScript},
		Dir:             dir,
		Env:             testEnv(),
		Tty:             true,
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

	// What is under test is the run's behaviour while a process still holds the
	// PTY slave, so nothing may take that process away: the sweep would remove
	// the helper and the read would end on its own.
	m.sweepFn = func(_ context.Context, _ *slog.Logger, _ int) int { return 0 }

	// The run's error comes back to the test goroutine: a failed assertion calls
	// FailNow, which only the test goroutine may do.
	runErr := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := m.runOnce(t.Context())
		runErr <- err
	}()

	select {
	case err := <-runErr:
		assert.NilError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the run never ended, so it is waiting on the reader the helper keeps blocked")
	}
	elapsed := time.Since(started)
	// The command exits at once and the helper lives for five minutes, so the
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

	// The second run releases the helper and waits for it to have written to the
	// first run's PTY, so the reader left behind hands its output over while
	// this run owns the scrollback, the log and the screen.
	secondScript := fmt.Sprintf(
		`: > '%s'
until [ -e '%s' ]; do sleep 0.05; done
sleep 0.5
echo second-run
`,
		triggerPath, wrotePath,
	)
	secondCfg := *cfg
	secondCfg.Argv = []string{"/bin/sh", "-c", secondScript}
	m.cfg = &secondCfg

	go func() {
		_, err := m.runOnce(t.Context())
		runErr <- err
	}()

	select {
	case err := <-runErr:
		assert.NilError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the second run never ended")
	}

	// Without this the assertions below would pass on a helper that never wrote
	// anything at all.
	_, err = os.Stat(wrotePath)
	assert.NilError(t, err, "the helper never wrote to the PTY of the run it outlived")

	scrollback := string(m.ring.Bytes())
	assert.Assert(t, strings.Contains(scrollback, "second-run"), "scrollback: %q", scrollback)
	assert.Assert(
		t,
		!strings.Contains(scrollback, staleOutputMarker),
		"the scrollback took output from the reader the first run left behind: %q",
		scrollback,
	)

	consoleLog, err := os.ReadFile(filepath.Join(commandDir, k8sfile.DefaultLogFileName))
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(consoleLog), "second-run"))
	assert.Assert(
		t,
		!strings.Contains(string(consoleLog), staleOutputMarker),
		"the log took output from the reader the first run left behind: %q",
		string(consoleLog),
	)

	// Warnings describe the latest run, so the run that started here took the
	// record over from the one that reported the blocked reader.
	_, _, stateJSON, err = st.GetCommandState(id)
	assert.NilError(t, err)
	assert.Assert(
		t,
		len(stateJSON.Warnings) == 0,
		"the second run kept the first run's warnings: %v",
		stateJSON.Warnings,
	)
}

// killPidFile kills the process named by the pid file, if it is still around.
func killPidFile(t *testing.T, pidPath string) {
	t.Helper()
	b, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return
	}
	// The helper ignores TERM, and an ignored disposition survives the exec it
	// does, so KILL is the only signal that ends it.
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// lastEventOfType returns the last event of typ in the JSONL event log.
func lastEventOfType(t *testing.T, path string, typ model.EventType) model.Event {
	t.Helper()
	f, err := os.Open(path)
	assert.NilError(t, err)
	defer f.Close()

	var (
		found model.Event
		ok    bool
	)
	dec := json.NewDecoder(f)
	for {
		var e model.Event
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			break
		}
		assert.NilError(t, err)
		if e.Type == typ {
			found, ok = e, true
		}
	}
	assert.Assert(t, ok, "no %s event in %s", typ, path)
	return found
}

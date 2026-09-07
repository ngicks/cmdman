package monitor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	cmdstore "github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

// Default PTY window size for a TTY-backed command before any client resizes it,
// so a full-screen program renders at a conventional size even with no attach.
const (
	defaultPtyRows uint16 = 24
	defaultPtyCols uint16 = 80
)

// readerDrainWait bounds how long a run waits for its PTY reader once it has
// closed the master. The reader sits in a blocking read that ends only when
// every process holding the slave has let go of it, and closing the master
// does not wake it, so a process the command left behind keeps it parked for
// as long as it lives. Waiting a moment lets the output a command ended with
// reach the log before the exit event; giving up after it keeps a command
// whose own process is gone from being reported as running forever.
const readerDrainWait = 1 * time.Second

// runAnomaly is something that went wrong as a run ended without keeping the
// run from ending: attr flags it on the run's terminal event, msg says what
// happened in the command state.
type runAnomaly struct {
	attr string
	msg  string
}

var anomalyReaderDetached = runAnomaly{
	attr: "reader_detached",
	msg:  fmt.Sprintf("pty reader still blocked after %s", readerDrainWait),
}

// noteRunAnomaly records an anomaly of the run being torn down.
func (m *Monitor) noteRunAnomaly(a runAnomaly) {
	m.runAnomalies = append(m.runAnomalies, a)
}

// runAnomalyAttrs renders the latest run's anomalies as event attributes, nil
// when it had none.
func (m *Monitor) runAnomalyAttrs() map[string]string {
	if len(m.runAnomalies) == 0 {
		return nil
	}
	attrs := make(map[string]string, len(m.runAnomalies))
	for _, a := range m.runAnomalies {
		attrs[a.attr] = "true"
	}
	return attrs
}

// runAnomalyWarnings renders the same anomalies for the persisted state, nil
// when the run had none.
func (m *Monitor) runAnomalyWarnings() []string {
	if len(m.runAnomalies) == 0 {
		return nil
	}
	msgs := make([]string, len(m.runAnomalies))
	for i, a := range m.runAnomalies {
		msgs[i] = a.msg
	}
	return msgs
}

// RunMonitor is the main entry point for the monitor process.
// It reads config, starts the command, and serves gRPC until the command exits.
func RunMonitor(
	ctx context.Context,
	id string,
	cfg config.Config,
	logger *slog.Logger,
) error {
	m, err := newMonitor(ctx, id, cfg, logger)
	if err != nil {
		return err
	}
	defer m.Close()

	if err := m.init(); err != nil {
		return err
	}

	if err := m.listen(); err != nil {
		return err
	}

	return m.start(ctx)
}

func (m *Monitor) runLoop(ctx context.Context) (err error) {
	// The supervisor calls GracefulStop once this returns, and that waits for
	// every in-flight RPC. Attach, Subscribe and WatchRuntimeState all park on
	// one of these two broadcasters, so an exit that left either open would
	// hold shutdown open forever. Both Close calls are idempotent; the paths
	// below still close outputBridge first, where closing it before the exit is
	// announced lets a viewer drain the output the run ended with.
	defer func() {
		m.outputBridge.Close()
		m.stateChangeBridge.Close()
	}()

	org := m.stateJSON.RestartCount
	for ; ; m.stateJSON.RestartCount++ {
		if m.stateJSON.RestartCount > org {
			m.Logger.Info("restarting command", slog.Int("restart_count", m.stateJSON.RestartCount))
			m.emitEvent(model.Event{
				Time: time.Now().UTC(),
				Type: model.EventTypeStarting,
				ID:   m.ID,
				Attrs: map[string]string{
					"restart_count": fmt.Sprintf("%d", m.stateJSON.RestartCount),
				},
			})
		} else {
			m.emitEvent(model.Event{
				Time:  time.Now().UTC(),
				Type:  model.EventTypeStarting,
				ID:    m.ID,
				State: model.EventTypeStarting,
			})
		}

		// Re-read config on each restart iteration.
		cfg, err := cmdstore.ReadCommandConfig(m.CommandDir)
		if err != nil {
			err = fmt.Errorf("read config: %w", err)
			m.setFailed(err.Error())
			return err
		}
		m.cfg = cfg

		exitCode, err := m.runOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				m.outputBridge.Close()
				m.setExited(-1)
				return nil
			}
			m.setFailed(fmt.Sprintf("run failed: %v", err))
			return err
		}

		m.stateJSON.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = m.store.InsertCommandExitCode(m.ID, exitCode)

		switch m.cfg.RestartPolicy {
		case model.RestartPolicyNo:
		case model.RestartPolicyOnFailure:
			if exitCode != 0 && !m.stopRequested.Load() && ctx.Err() == nil {
				if m.cfg.MaxRetries == 0 || m.stateJSON.RestartCount < m.cfg.MaxRetries {
					continue
				}
			}
		case model.RestartPolicyAlways:
			if !m.stopRequested.Load() && ctx.Err() == nil {
				continue
			}
		}

		m.outputBridge.Close()
		m.setExited(exitCode)
		return m.maybeAutoRemove()
	}
}

func (m *Monitor) wireUpCmd(ctx context.Context) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, m.cfg.Argv[0], m.cfg.Argv[1:]...)
	cmd.Dir = m.cfg.Dir
	cmd.Env = config.WithCommandContextEnv(m.cfg.Env, m.Config, m.ID, m.cfg.CommandDir)
	if len(cmd.Env) == 0 {
		return nil, fmt.Errorf("command config env is empty")
	}
	// Place the child in its own process group and route ctx cancellation
	// through a group-wide signal so grandchildren (e.g. `sleep` under
	// `sh -c "sleep 300"`) are reached too.
	prepProcessAttrs(cmd, m.cfg.Tty)
	cmd.WaitDelay = 10 * time.Second

	return cmd, nil
}

func (m *Monitor) openLogWriter(ctx context.Context) (logdriver.Writer, error) {
	opts := maps.Clone(m.cfg.LogOpts)
	logWriter, err := logdriver.NewWriter(
		ctx,
		string(m.cfg.LogDriver),
		m.cfg.CommandDir,
		opts,
	)
	if err != nil {
		return nil, fmt.Errorf("open log writer: %w", err)
	}
	return logWriter, nil
}

func (m *Monitor) runOnce(ctx context.Context) (int, error) {
	cmd, err := m.wireUpCmd(ctx)
	if err != nil {
		return -1, err
	}

	// Hook config is re-resolved per run, like the command config it comes
	// from. Hooks get the command's own environment, so they inherit the
	// CMDMAN_CMD_ID family without a second construction of it.
	m.hooks.configure(
		model.HookLayers{Command: m.cfg.Hooks, Global: m.Config.DefaultHooks},
		m.cfg.Dir,
		cmd.Env,
	)

	logWriter, err := m.openLogWriter(ctx)
	if err != nil {
		return -1, err
	}

	defer func() {
		if cerr := logWriter.Close(); cerr != nil {
			m.Logger.Warn("close log writer", slog.String("error", cerr.Error()))
		}
	}()
	m.outputMu.Lock()
	m.logWriter = logWriter
	// Every byte this run produces is tagged with the generation read here, so
	// output that arrives once the run has given the shared state up is
	// recognizable as stale.
	gen := m.runGen
	m.terminalState.reset()
	// The run that ended cleared its own runtime state; this repeats it for the
	// first run, and is a no-op otherwise. Seeding the configured directory
	// right after gives every run a cwd before the child produces a byte, and a
	// restart re-seeds from the config this iteration re-read.
	m.runtimeState.reset()
	m.runtimeState.seedCwd(m.cfg.Dir)
	m.outputMu.Unlock()
	defer func() {
		m.outputMu.Lock()
		if m.logWriter == logWriter {
			m.logWriter = nil
		}
		m.outputMu.Unlock()
	}()

	var (
		ptmx   *os.File
		stdin  io.WriteCloser
		waitFn func()
	)
	if m.cfg.Tty {
		ptmx, stdin, waitFn, err = m.writeTty(ctx, cmd, gen)
	} else {
		stdin, waitFn, err = m.wirePipe(cmd, gen)
	}

	if err != nil {
		return -1, err
	}

	// The handles belong to this run and only exist between these two sections,
	// which is what the RPC-facing readers hold procMu to observe.
	m.procMu.Lock()
	m.ptmx, m.stdin, m.cmd = ptmx, stdin, cmd
	m.procMu.Unlock()

	m.setRunning()

	err = cmd.Wait()

	m.procMu.Lock()
	m.ptmx, m.stdin, m.cmd = nil, nil, nil
	m.procMu.Unlock()

	waitFn()

	// Runtime state dies with the run (D13). Clearing it here rather than only
	// when the next run gets this far is what keeps a dead run's title, bell or
	// reported status from being served over Status and WatchRuntimeState in
	// between - or for good, when the next run's setup fails. reset signals its
	// own change, so watchers see the cleared state. runtimeState carries its
	// own lock and is read without outputMu, so this takes neither.
	m.runtimeState.reset()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// writeTty starts cmd on a PTY and hands the run's handles and teardown back to
// runOnce, which is where they are published. Output it reads is tagged with
// gen so the shared output state can tell it apart from a later run's.
func (m *Monitor) writeTty(
	ctx context.Context,
	cmd *exec.Cmd,
	gen uint64,
) (*os.File, io.WriteCloser, func(), error) {
	ptmx, err := startTty(cmd)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pty start: %w", err)
	}
	// Give the PTY a conventional default size so a full-screen program renders
	// sanely even when no interactive client ever attaches to resize it.
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: defaultPtyRows, Cols: defaultPtyCols})

	// Start a fresh server-side screen mirror for this run (a restart gets a clean
	// screen). Sized to the default PTY; Monitor.Resize keeps it in sync.
	m.outputMu.Lock()
	m.screen.close()
	m.screen = newScreenTracker(int(defaultPtyCols), int(defaultPtyRows), m.runtimeState)
	m.outputMu.Unlock()

	// readerDone is closed by the reader itself because the teardown below
	// waits on it with a deadline, which neither errgroup nor a WaitGroup
	// offers.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				m.logCommandOutput(gen, logdriver.StreamStdout, data)
			}
			if err != nil {
				return
			}
		}
	}()

	return ptmx, ptmx, func() {
		// Closing the master is what ends the read for a command that took its
		// whole process tree with it. It is best-effort: the read only returns
		// once the last holder of the slave is gone, so a process that escaped
		// the command keeps this reader parked. The run must not wait on that,
		// so it waits a moment for the trailing output and then leaves the
		// reader behind rather than joining it.
		_ = ptmx.Close()
		select {
		case <-readerDone:
		case <-time.After(readerDrainWait):
			m.noteRunAnomaly(anomalyReaderDetached)
			contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
				ctx,
				"pty reader still blocked; leaving it behind",
				slog.String("id", m.ID),
				slog.Duration("waited", readerDrainWait),
			)
		}
		// Past this point the reader speaks for a run that is over, and what it
		// reads is dropped instead of reaching the next run.
		m.outputMu.Lock()
		m.runGen++
		m.outputMu.Unlock()
	}, nil
}

// wirePipe starts cmd on pipes and hands the run's stdin and teardown back to
// runOnce, which is where they are published. There is no PTY on this path, so
// no reader can outlive the run; the writers still tag their output with gen so
// output reaches the shared state through one rule.
func (m *Monitor) wirePipe(cmd *exec.Cmd, gen uint64) (io.WriteCloser, func(), error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	cmd.Stdout = &monitorOutputWriter{
		monitor: m,
		gen:     gen,
		stream:  logdriver.StreamStdout,
	}
	cmd.Stderr = &monitorOutputWriter{
		monitor: m,
		gen:     gen,
		stream:  logdriver.StreamStderr,
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start command: %w", err)
	}

	return stdin, func() {}, nil
}

// logCommandOutput fans one chunk of output out to the scrollback, the log
// driver, live subscribers and the screen mirror. gen names the run the chunk
// came from: output of a run that has already given the shared state up is
// dropped here, which is the single point every producer goes through.
func (m *Monitor) logCommandOutput(gen uint64, stream logdriver.Stream, data []byte) {
	if len(data) == 0 {
		return
	}
	lines := logdriver.SplitLogLines(time.Now(), stream, data)
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	if gen != m.runGen {
		return
	}
	if m.cfg.Tty {
		m.terminalState.Observe(data)
	}
	m.ring.Write(data)
	for _, line := range lines {
		if m.logWriter != nil {
			if err := m.logWriter.WriteLogLine(line); err != nil {
				m.Logger.Warn("log writer", slog.String("error", err.Error()))
			}
		}
		m.outputBridge.Send(line)
	}
	// Mirror TTY output into the server-side screen last, so a vt hazard never
	// delays the ring/log/broadcaster writes above. feed is panic-guarded.
	if m.cfg.Tty {
		m.screen.feed(data)
	}
}

type monitorOutputWriter struct {
	monitor *Monitor
	gen     uint64
	stream  logdriver.Stream
}

func (w *monitorOutputWriter) Write(data []byte) (int, error) {
	buf := make([]byte, len(data))
	copy(buf, data)
	w.monitor.logCommandOutput(w.gen, w.stream, buf)
	return len(data), nil
}

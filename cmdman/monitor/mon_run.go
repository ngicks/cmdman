package monitor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"strconv"
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

// readerDrainWait bounds how long a run waits for the readers of its output
// once the command is reaped and the survivor sweep has finished. A reader sits
// in a blocking read that ends only when every process holding the other end
// has let go of it, so a process the command left behind keeps it parked for as
// long as it lives. Waiting a moment lets the output a command ended with reach
// the log before the exit event; giving up after it keeps a command whose own
// process is gone from being reported as running forever.
const readerDrainWait = 1 * time.Second

// runAnomaly is something that went wrong as a run ended without keeping the
// run from ending: attr flags it on the run's terminal event, msg says what
// happened in the command state. An anomaly that counts something puts the
// count in value; one that only says "this happened" leaves value empty and
// reads as "true" on the event.
type runAnomaly struct {
	attr  string
	value string
	msg   string
}

// anomalyReaderDetached reports output the run gave up on: a reader still
// parked when the drain bound expired, which both the PTY and the pipe path
// report the same way because both mean the same thing to a user - trailing
// output may be missing.
var anomalyReaderDetached = runAnomaly{
	attr: "reader_detached",
	msg:  fmt.Sprintf("output reader still blocked after %s", readerDrainWait),
}

// anomalySurvivorsUnreaped reports the processes the command left behind that
// the sweep could not get rid of within its bound.
func anomalySurvivorsUnreaped(n int) runAnomaly {
	return runAnomaly{
		attr:  "survivors_unreaped",
		value: strconv.Itoa(n),
		msg:   fmt.Sprintf("%d survivor(s) still alive after sweep bound", n),
	}
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
		value := a.value
		if value == "" {
			value = "true"
		}
		attrs[a.attr] = value
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
	// Everything the command leaves behind is reparented to the monitor instead
	// of to init, which is what lets a run enumerate and terminate what it
	// spawned. Losing this only costs the sweep its reach, so a monitor that
	// cannot become a subreaper still supervises its command.
	if err := becomeSubreaper(); err != nil {
		logger.WarnContext(ctx, "become subreaper", slog.String("error", err.Error()))
	}

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

	var exitCode int
	for first := true; ; first = false {
		if !first {
			// The policy below says whether the command should run again;
			// whether it may is decided here, which is as late as the loop can
			// still decide it. A stop landing after a run has ended has only
			// this flag to speak through - there is no process left to carry
			// its signal - and reading the flag alongside the policy instead
			// would let the restart it raced swallow it. The counter moves
			// after the check, so a restart a stop cancelled is never counted
			// as one.
			if m.stopRequested.Load() {
				break
			}
			m.stateJSON.RestartCount++
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

		exitCode, err = m.runOnce(ctx)
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

		restart := false
		switch m.cfg.RestartPolicy {
		case model.RestartPolicyNo:
		case model.RestartPolicyOnFailure:
			if exitCode != 0 && ctx.Err() == nil {
				restart = m.cfg.MaxRetries == 0 || m.stateJSON.RestartCount < m.cfg.MaxRetries
			}
		case model.RestartPolicyAlways:
			restart = ctx.Err() == nil
		}
		if !restart {
			break
		}
	}

	m.outputBridge.Close()
	m.setExited(exitCode)
	return m.maybeAutoRemove()
}

func (m *Monitor) wireUpCmd(ctx context.Context) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, m.cfg.Argv[0], m.cfg.Argv[1:]...)
	cmd.Dir = m.cfg.Dir
	cmd.Env = config.WithCommandContextEnv(m.cfg.Env, m.Config, m.ID, m.cfg.CommandDir)
	if len(cmd.Env) == 0 {
		return nil, fmt.Errorf("command config env is empty")
	}
	// Place the child in its own session and route ctx cancellation through a
	// group-wide signal so grandchildren (e.g. `sleep` under
	// `sh -c "sleep 300"`) are reached too.
	prepCommandAttrs(cmd)
	// WaitDelay bounds the cancellation wired just above: exec signals the
	// group and, when the command is still there once this expires, kills it,
	// so a command that ignores the signal cannot hold the monitor's shutdown
	// open. It no longer bounds output - the monitor reads the command's output
	// itself on both paths, so exec has no copying of its own left to wait for.
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
	// Anomalies and warnings describe the run they were recorded for, so the run
	// starting here takes the record over from the one before it before anything
	// can fail. Setup that fails ahead of setRunning (empty env, log writer, the
	// output pipes) then reports on its own failed event instead of replaying the
	// previous run's anomalies.
	m.runAnomalies = nil
	m.stateJSON.Warnings = nil

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
		stdin, waitFn, err = m.wirePipe(ctx, cmd, gen)
	}

	if err != nil {
		return -1, err
	}

	// The child leads its own session, so its pid is also the id of the process
	// group everything it spawns starts out in.
	pgid := cmd.Process.Pid

	// The handles belong to this run and only exist between these two sections,
	// which is what the RPC-facing readers hold procMu to observe.
	m.procMu.Lock()
	m.ptmx, m.stdin, m.cmd, m.runPgid = ptmx, stdin, cmd, pgid
	m.procMu.Unlock()

	m.setRunning()

	err = cmd.Wait()

	m.procMu.Lock()
	m.ptmx, m.stdin, m.cmd = nil, nil, nil
	m.procMu.Unlock()

	m.sweepSurvivors(ctx, pgid)

	waitFn()

	// The group id is given up only here: until the sweep and the drain above
	// are done the run still has processes a stop can be aimed at, and refusing
	// a signal in that window is what kept an escalation from ever reaching
	// them.
	m.procMu.Lock()
	m.runPgid = 0
	m.procMu.Unlock()

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

// sweepSurvivors terminates and reaps the processes the command left in its own
// session - a helper that outlived the process that started it, say - so they
// stop holding the port, the file or the terminal they were given instead of
// stacking up across restarts. A process that made a session of its own is left
// alone: a deliberately detached daemon (a shared multiplexer server the run
// must never tear down is the motivating case), or a grandchild that broke away
// and reverts to the same accepted, unreaped cost as on a non-Linux host.
//
// It runs before the output teardown on purpose: a read parked on the pty
// master or on a pipe only ends once the last holder of the other end has let
// go of it, so clearing the in-session holders is what lets the drain that
// follows finish rather than time out. An escapee that kept a handle open is
// left to that drain, which detaches the reader after a bounded wait instead of
// waiting on the handle for good.
func (m *Monitor) sweepSurvivors(ctx context.Context, pgid int) {
	sweep := m.sweepFn
	if sweep == nil {
		sweep = sweepRunSurvivors
	}
	logger := contextkey.ValueSlogLoggerDefault(ctx)

	// The run's own context is already cancelled when the monitor is shutting
	// down, which is exactly when what the command left behind matters most, so
	// the sweep drops that cancellation while keeping the context's values (the
	// logger among them). Its timeout is the sweep's bound: a process in an
	// uninterruptible wait ignores SIGKILL as well, and the run has to end
	// regardless.
	sweepCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sweepBound)
	defer cancel()

	unreaped := sweep(sweepCtx, logger, pgid)
	if unreaped == 0 {
		return
	}
	m.noteRunAnomaly(anomalySurvivorsUnreaped(unreaped))
	logger.WarnContext(
		sweepCtx,
		"processes the command left behind are still alive",
		slog.String("id", m.ID),
		slog.Int("count", unreaped),
	)
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

	readerDone := m.readOutput(gen, ptmx, logdriver.StreamStdout)

	return ptmx, ptmx, func() {
		// Closing the master is what ends the read for a command that took its
		// whole process tree with it. It is best-effort: the read only returns
		// once the last holder of the slave is gone, so a process that escaped
		// the command keeps this reader parked, and nothing this side can wake
		// it.
		_ = ptmx.Close()
		m.awaitReaders(ctx, readerDone)
	}, nil
}

// wirePipe starts cmd on pipes the monitor reads itself and hands the run's
// stdin and teardown back to runOnce, which is where they are published.
//
// The pipes are made here rather than left to os/exec: exec hands an *os.File
// to the child as it is and starts no copying goroutine for it, so cmd.Wait
// returns as soon as the command is reaped. Letting exec make them instead
// would make Wait join those goroutines, and a process the command left behind
// that inherited its stdout keeps them going, which held every run with a
// leftover open until exec's own delay expired and then failed it. Output is
// tagged with gen so it reaches the shared state through one rule.
func (m *Monitor) wirePipe(
	ctx context.Context,
	cmd *exec.Cmd,
	gen uint64,
) (io.WriteCloser, func(), error) {
	// The two output pipes are made before the stdin pipe so nothing that can
	// fail sits between StdinPipe and Start: StdinPipe opens a descriptor that
	// only Start (or Wait) closes, so a later os.Pipe failure would return with
	// that stdin pipe leaked.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
		return nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	startErr := cmd.Start()
	// The child has descriptors of its own now and exec leaves these two to
	// whoever made them, so the monitor's copies of the write ends go here: a
	// read ends only once nobody holds the write end at all, this process
	// included.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	if startErr != nil {
		_ = stdoutR.Close()
		_ = stderrR.Close()
		return nil, nil, fmt.Errorf("start command: %w", startErr)
	}

	stdoutDone := m.readOutput(gen, stdoutR, logdriver.StreamStdout)
	stderrDone := m.readOutput(gen, stderrR, logdriver.StreamStderr)

	return stdin, func() {
		m.awaitReaders(ctx, stdoutDone, stderrDone)
		// A pipe wakes the read parked on it when its read end closes, which a
		// pty master cannot do, so a process that outlived the command never
		// pins these readers for the rest of the monitor's life. It happens
		// here and not in the readers themselves because this is the side that
		// knows the run is over and has already given up on the output.
		_ = stdoutR.Close()
		_ = stderrR.Close()
	}, nil
}

// readOutput reads f to its end on a goroutine of its own and hands every chunk
// to the run's output tagged with gen. The channel it returns is closed once
// the read is over; it is a channel and not an errgroup or a WaitGroup because
// the teardown waits on it with a bound instead of joining it.
func (m *Monitor) readOutput(
	gen uint64,
	f *os.File,
	stream logdriver.Stream,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				m.logCommandOutput(gen, stream, data)
			}
			if err != nil {
				return
			}
		}
	}()
	return done
}

// awaitReaders gives the readers of a finished run a moment to deliver the
// output the command ended with, then closes the run's output for good. A
// reader that has not finished by then is left behind rather than joined:
// waiting on one a leftover process keeps blocked would report a command whose
// own process is long gone as running forever.
func (m *Monitor) awaitReaders(ctx context.Context, readers ...<-chan struct{}) {
	// The run's own context is already cancelled when the monitor is shutting
	// down, so a bound derived from it would expire at once and drop the output
	// the command ended with. Dropping the cancellation while keeping the
	// context's values gives the drain its own clock and still carries the
	// logger the warning below reads.
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readerDrainWait)
	defer cancel()

	detached := false
	for _, done := range readers {
		select {
		case <-done:
		case <-drainCtx.Done():
			detached = true
		}
	}
	if detached {
		m.noteRunAnomaly(anomalyReaderDetached)
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
			ctx,
			"output reader still blocked; leaving it behind",
			slog.String("id", m.ID),
			slog.Duration("waited", readerDrainWait),
		)
	}
	// Past this point a reader still going speaks for a run that is over, and
	// what it reads is dropped instead of reaching the next run.
	m.outputMu.Lock()
	m.runGen++
	m.outputMu.Unlock()
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

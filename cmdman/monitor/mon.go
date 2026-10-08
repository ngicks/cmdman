// Package monitor implements the per-command monitor process: it supervises a
// single child command, serves the command's gRPC control socket, and manages
// scrollback, log fan-out, terminal emulation, and restart policy. The Service
// in cmdman spawns one detached monitor per command.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"google.golang.org/grpc"

	pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/cmdman/eventlog"
	"github.com/ngicks/cmdman/cmdman/internal/flock"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	cmdstore "github.com/ngicks/cmdman/cmdman/store"
)

// Monitor is the per-command monitor process.
type Monitor struct {
	ID         string
	CommandDir string
	DBPath     string
	Config     config.Config
	Logger     *slog.Logger

	cleanUp []func() error

	store     *cmdstore.Store
	cfg       *model.CommandConfig
	stateJSON *model.CommandState
	evtLog    *eventlog.Writer

	lis net.Listener

	// procMu guards the per-run process state below. runOnce publishes ptmx,
	// stdin and cmd once the child is wired and clears them once it has been
	// reaped, so an RPC arriving either side of a run sees the whole trio change
	// at once instead of a half-torn-down run. It is never held across a write
	// to the child.
	//
	// runPgid is the process group the command led, and it outlives that trio on
	// purpose: the handles go away the moment the child is reaped, while the
	// survivor sweep and the drain of the command's output that follow keep the
	// run going for a while yet. A stop escalating to SIGKILL in that window still has to reach
	// whatever the command left behind, so the group id is cleared only once the
	// run is really over.
	//
	// stopDeadline is the SIGKILL a stop with a timeout has scheduled. The run
	// disarms it where it gives runPgid up, so a deadline that outlives its run
	// never lands on a pid handed out again.
	//
	// stopSeq is the run's stop sequence, nil for a command without a stop
	// command. The run publishes it with the handles and gives it up with
	// runPgid, once a sequence a stop began has finished: the signal that
	// follows the stop command is aimed at runPgid and may arm stopDeadline.
	//
	// gracefulStop is set by a stop with a signal other than SIGKILL that lands
	// during the run, and the run resets it as it begins. A SIGKILL stop that
	// finds it set is the escalation of that stop rather than a SIGKILL asked
	// for in its own right.
	procMu       sync.Mutex
	ptmx         *os.File
	stdin        io.WriteCloser
	cmd          *exec.Cmd
	runPgid      int
	stopDeadline *time.Timer
	stopSeq      *stopSequence
	gracefulStop bool
	// stdinWriteMu serializes the stdin writes themselves, so two clients
	// writing at once never interleave bytes inside a chunk.
	stdinWriteMu sync.Mutex
	ring         *ringBuffer

	outputMu          sync.Mutex
	outputBridge      *broadcaster[logdriver.LogLine]
	stateChangeBridge *broadcaster[monitorStateChange]
	logWriter         logdriver.Writer
	terminalState     *terminalPaneState
	// runGen names the run whose output the shared state above still accepts.
	// A run captures it when it wires its command and the run's teardown bumps
	// it, so output arriving from a reader that outlived its run is dropped
	// instead of landing in the next run's scrollback, log or screen.
	runGen uint64
	// runtimeState latches what a TTY command reports about itself (title,
	// bell, notifications). It is per-run state, reset by runOnce, and is read
	// without outputMu - see commandRuntimeState.
	runtimeState *commandRuntimeState
	// hooks runs the configured argv hooks for what runtimeState captures and
	// answers which sequences must be kept from viewers (D17/D40).
	hooks *hookDispatcher
	// screen mirrors a TTY command's output in a server-side emulator so an
	// attaching client gets a coherent current-screen snapshot instead of raw
	// scrollback that may have rotated mid-sequence. nil for non-TTY commands.
	screen *screenTracker

	grpcServer *grpc.Server
	sockPath   string

	// wg tracks per-request goroutines spawned by RPC handlers (e.g. the
	// Attach stream-recv pump). RPC handlers that need to spawn a helper
	// goroutine register it here instead of joining inside the handler.
	// The supervisor waits on this group between GracefulStop (which is
	// what unblocks gRPC Recv calls) and resource teardown.
	wg sync.WaitGroup

	// runAnomalies collects what went wrong as the latest run ended. It is
	// written and read on the goroutine that drives the run - the one that
	// tears a run down and the one that publishes its outcome - so it needs no
	// lock of its own.
	runAnomalies []runAnomaly

	// sweepFn terminates the processes a finished run left in the command's own
	// session and reports how many outlived the sweep, or sweepHandedOver once
	// stopRequested reports a stop. awaitFn waits those processes out while a
	// stop is in progress and reports how many it left alive. They are fields
	// so a test can drive the giving-up paths without a process that genuinely
	// refuses to die, and see what either sends. nil runs the default.
	sweepFn func(ctx context.Context, logger *slog.Logger, pgid int, stopRequested func() bool) int
	awaitFn func(ctx context.Context, logger *slog.Logger, pgid int, killed func() bool) int
	// stopSignalFn sends the stop signal that a stop sequence follows its stop
	// command with to the run's process group. It is a field so a test can see
	// whether the sequence sent one at all. nil sends it with
	// signalProcessGroup.
	stopSignalFn func(pgid int, sig syscall.Signal) error

	// stopRequested is set by a stop to prevent restarts. Nothing clears it: the
	// loop ends on the first run end that sees it, and the monitor exits with it.
	stopRequested atomic.Bool
	// stopKilled is set once a stop's own SIGKILL is on its way, whether the
	// client sent it or the deadline the monitor armed for the stop did. It is
	// what lets the run end finish that SIGKILL for what the process group could
	// not reach. Like stopRequested it is never cleared.
	stopKilled atomic.Bool
	// forceKilled is set once a graceful stop of the run ran out its grace
	// period and the SIGKILL that followed reached the run: the deadline the
	// stop armed, or the client's own SIGKILL once its wait was over. A SIGKILL
	// asked for in its own right leaves it alone. It is only ever set under
	// procMu, and the run resets it as it begins.
	forceKilled atomic.Bool
}

func newMonitor(
	ctx context.Context,
	id string,
	cfg config.Config,
	logger *slog.Logger,
) (*Monitor, error) {
	commandDir, err := cfg.CommandDir(id)
	if err != nil {
		return nil, err
	}

	dbPath, err := cfg.DBPath()
	if err != nil {
		return nil, err
	}

	st, err := cmdstore.OpenStore(ctx, dbPath, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	commandCfg, err := cmdstore.ReadCommandConfig(commandDir)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("read config: %w", err)
	}

	var evtLog *eventlog.Writer
	if eventPath, err := cfg.EventLogPath(); err == nil {
		if w, werr := eventlog.NewWriter(eventPath); werr == nil {
			evtLog = w
		} else {
			logger.Warn("eventlog: open writer", slog.String("error", werr.Error()))
		}
	} else {
		logger.Warn("eventlog: resolve path", slog.String("error", err.Error()))
	}

	hooks := newHookDispatcher(logger)
	runtimeState := newCommandRuntimeState()
	runtimeState.setHookSink(hooks.dispatch)

	return &Monitor{
		ID:                id,
		CommandDir:        commandDir,
		DBPath:            dbPath,
		Config:            cfg,
		Logger:            logger,
		outputBridge:      newBroadcaster[logdriver.LogLine](),
		stateChangeBridge: newBroadcaster[monitorStateChange](),
		terminalState:     newTerminalPaneState(),
		runtimeState:      runtimeState,
		hooks:             hooks,
		store:             st,
		cfg:               commandCfg,
		evtLog:            evtLog,
		sweepFn:           sweepRunSurvivors,
		awaitFn:           awaitRunSurvivors,
		ring:              newRingBuffer(commandCfg.ScrollbackBytes),
		stateJSON: &model.CommandState{
			MonitorPID: os.Getpid(),
		},
		cleanUp: []func() error{
			st.Close,
		},
	}, nil
}

// emitEvent appends an event from the monitor side, best-effort.
func (m *Monitor) emitEvent(e model.Event) {
	if m.evtLog == nil {
		return
	}
	if err := m.evtLog.Append(e); err != nil {
		m.Logger.Warn("eventlog: append",
			slog.String("type", string(e.Type)),
			slog.String("id", e.ID),
			slog.String("error", err.Error()),
		)
	}
}

func (m *Monitor) Close() error {
	// End the screen mirror's response-pipe drain goroutine (see screenTracker).
	m.outputMu.Lock()
	m.screen.close()
	m.screen = nil
	m.outputMu.Unlock()

	var errs []error
	for _, c := range slices.Backward(m.cleanUp) {
		err := c()
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (m *Monitor) init() (err error) {
	var cleanUp []func() error
	defer func() {
		if err != nil {
			for _, c := range slices.Backward(cleanUp) {
				c()
			}
		}
	}()

	// lock pid first
	pidPath, err := m.Config.MonitorPIDPath(m.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}
	f, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	cleanUp = append(
		cleanUp,
		f.Close,
	)

	acquired, err := flock.TryLockExclusive(f)
	if err != nil {
		return fmt.Errorf("lock pid file %q: %w", pidPath, err)
	}
	if !acquired {
		return fmt.Errorf("monitor %q already running: pid file %q is locked", m.ID, pidPath)
	}
	cleanUp = append(
		cleanUp,
		func() error { return flock.Unlock(f) },
		func() error { return os.Remove(pidPath) },
	)

	if err := m.store.UpdateCommandState(
		m.ID,
		model.EventTypeStarting,
		nil,
		m.stateJSON,
	); err != nil {
		return fmt.Errorf("update state to starting: %w", err)
	}

	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Write([]byte(strconv.Itoa(os.Getpid()))); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}

	// Start gRPC server.
	m.sockPath, err = m.Config.MonitorSocketPath(m.ID)
	if err != nil {
		return err
	}
	m.stateJSON.SocketPath = m.sockPath
	if err := m.store.UpdateCommandState(
		m.ID,
		model.EventTypeStarting,
		nil,
		m.stateJSON,
	); err != nil {
		return fmt.Errorf("update state with socket: %w", err)
	}

	m.cleanUp = append(m.cleanUp, cleanUp...)

	return nil
}

func (m *Monitor) listen() error {
	lis, err := listenMonitorSocket(m.sockPath)
	if err != nil {
		return fmt.Errorf("listen socket: %w", err)
	}
	m.lis = lis

	m.cleanUp = append(
		m.cleanUp,
		func() error {
			return os.Remove(m.sockPath)
		},
		func() error {
			return m.lis.Close()
		},
	)

	return nil
}

func (m *Monitor) start(ctx context.Context) error {
	m.grpcServer = grpc.NewServer()
	pb.RegisterCommandMonitorServiceServer(m.grpcServer, &monitorServer{monitor: m})

	go func() {
		if err := m.grpcServer.Serve(m.lis); err != nil {
			m.Logger.Error("grpc serve error", slog.String("error", err.Error()))
		}
	}()
	defer func() {
		// GracefulStop closes the listener and tears down active streams,
		// which is what unblocks any goroutine still parked in
		// stream.Recv() inside an RPC handler. Once that returns, every
		// helper goroutine registered on m.wg can finish, so wait on it
		// before any resource cleanup runs.
		m.grpcServer.GracefulStop()
		m.wg.Wait()
	}()

	// Handle SIGTERM for graceful shutdown.
	sigCtx, sigStop := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer sigStop()

	// Hooks live exactly as long as the supervision they report on: close
	// signals a hook still running when the monitor goes away.
	m.hooks.start(sigCtx)
	defer m.hooks.close()

	return m.runLoop(sigCtx)
}

type monitorStateChange struct {
	State    model.EventType
	ExitCode int
	Pid      int
}

func (m *Monitor) subscribeStateChange() (<-chan monitorStateChange, func()) {
	return m.stateChangeBridge.Subscribe()
}

func (m *Monitor) publishStateChange(state model.EventType, exitCode int) {
	// Read the handle under procMu like the other readers of the trio, so the
	// mutex owns every read of m.cmd even though these callers run on the run
	// goroutine and never race a teardown in practice.
	m.procMu.Lock()
	cmd := m.cmd
	m.procMu.Unlock()
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	m.stateChangeBridge.Send(monitorStateChange{
		State:    state,
		ExitCode: exitCode,
		Pid:      pid,
	})
}

func isMonitorActiveState(state model.EventType) bool {
	return state == model.EventTypeStarting || state == model.EventTypeRunning
}

func (m *Monitor) setRunning() {
	m.stateJSON.StartedAt = time.Now().UTC().Format(time.RFC3339)
	// runOnce already cleared the previous run's anomalies, warnings and forced
	// kill at its top, ahead of any setup that could fail, so nothing to reset
	// here.
	// Append the event before flipping the DB state so observers polling
	// state cannot see "running" without the corresponding event on disk.
	m.emitEvent(model.Event{
		Time:  time.Now().UTC(),
		Type:  model.EventTypeRunning,
		ID:    m.ID,
		State: model.EventTypeRunning,
	})
	if err := m.store.UpdateCommandState(
		m.ID,
		model.EventTypeRunning,
		nil,
		m.stateJSON,
	); err != nil {
		m.Logger.Error("update state to running failed", slog.String("error", err.Error()))
	}
	m.publishStateChange(model.EventTypeRunning, 0)
}

func (m *Monitor) setExited(exitCode int) {
	ec := exitCode
	// Append the exit event before flipping the DB state so observers
	// that wait for state="exited" are guaranteed to find the event on
	// disk, not racing with a still-in-flight Append.
	m.emitEvent(model.Event{
		Time:     time.Now().UTC(),
		Type:     model.EventTypeExited,
		ID:       m.ID,
		State:    model.EventTypeExited,
		ExitCode: &ec,
		Attrs:    m.runAnomalyAttrs(),
	})
	m.stateJSON.Warnings = m.runAnomalyWarnings()
	m.stateJSON.ForceKilled = m.forceKilled.Load()
	_ = m.store.UpdateCommandState(m.ID, model.EventTypeExited, &exitCode, m.stateJSON)
	m.publishStateChange(model.EventTypeExited, exitCode)
	m.stateChangeBridge.Close()
}

func (m *Monitor) setFailed(errMsg string) {
	m.stateJSON.Error = errMsg
	// Same ordering rationale as setExited/setRunning.
	m.emitEvent(model.Event{
		Time:  time.Now().UTC(),
		Type:  model.EventTypeFailed,
		ID:    m.ID,
		State: model.EventTypeFailed,
		Error: errMsg,
		Attrs: m.runAnomalyAttrs(),
	})
	m.stateJSON.Warnings = m.runAnomalyWarnings()
	m.stateJSON.ForceKilled = m.forceKilled.Load()
	_ = m.store.UpdateCommandState(m.ID, model.EventTypeFailed, nil, m.stateJSON)
	m.publishStateChange(model.EventTypeFailed, 0)
	m.stateChangeBridge.Close()
}

func (m *Monitor) maybeAutoRemove() error {
	if m.cfg.Annotations[cmdstore.AnnotationAutoRemove] == "true" {
		m.Logger.Info("auto-removing command")
		if err := m.store.DeleteCommand(m.ID); err != nil {
			return fmt.Errorf("auto-remove db: %w", err)
		}
		if err := os.RemoveAll(m.cfg.CommandDir); err != nil {
			m.Logger.Warn("auto-remove dir failed", slog.String("error", err.Error()))
		}
	}
	return nil
}

// QueueStdin sends data to the running command's stdin.
func (m *Monitor) QueueStdin(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The handle lock is released before the write. A PTY write blocks for as
	// long as the child stops draining its input, and holding procMu across it
	// would freeze Resize, Status, Signal and Stop - Stop being the way out of
	// that state. Serialization moves to stdinWriteMu, which nothing else waits
	// on.
	m.procMu.Lock()
	stdin := m.stdin
	m.procMu.Unlock()
	if stdin == nil {
		return fmt.Errorf("no stdin")
	}
	m.stdinWriteMu.Lock()
	defer m.stdinWriteMu.Unlock()
	_, err := stdin.Write(data)
	return err
}

// Resize changes the PTY window size.
func (m *Monitor) Resize(rows, cols uint16) error {
	// Act on a copy: the run can end at any point during the call, and a handle
	// read out under the lock stays usable - a syscall on the closed file fails
	// with an error rather than reading a field mid-teardown.
	m.procMu.Lock()
	ptmx := m.ptmx
	m.procMu.Unlock()
	if ptmx == nil {
		return fmt.Errorf("no pty")
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols}); err != nil {
		return err
	}
	// Keep the server-side screen mirror at the command's real size so its
	// snapshot preserves the layout.
	m.outputMu.Lock()
	m.screen.resize(int(cols), int(rows))
	m.outputMu.Unlock()
	return nil
}

// PtySize returns the command's current PTY window size. ok is false for a
// non-TTY command (no PTY) so callers can skip reporting a size.
func (m *Monitor) PtySize() (rows, cols uint16, ok bool) {
	m.procMu.Lock()
	ptmx := m.ptmx
	m.procMu.Unlock()
	if ptmx == nil {
		return 0, 0, false
	}
	r, c, err := pty.Getsize(ptmx)
	if err != nil {
		return 0, 0, false
	}
	return uint16(r), uint16(c), true
}

// errNoRunningProcess is what a signal-carrying call gets when the command has
// nothing left to signal: no live child and no group from a run still winding
// down.
var errNoRunningProcess = errors.New("no running process")

// SignalProcess sends a raw signal to the running command and any
// descendants it has spawned within its process group.
func (m *Monitor) SignalProcess(sig syscall.Signal) error {
	m.procMu.Lock()
	cmd, pgid := m.cmd, m.runPgid
	m.procMu.Unlock()
	return signalRun(cmd, pgid, sig)
}

// signalRun sends sig to the process group of a run whose child is cmd and
// whose group is pgid, as read from the run's handles.
func signalRun(cmd *exec.Cmd, pgid int, sig syscall.Signal) error {
	if cmd != nil && cmd.Process != nil {
		return signalProcessGroup(cmd.Process.Pid, sig)
	}
	// The child is reaped but the run is not over: the survivor sweep and the
	// output-reader drain still have to finish, and they take long enough for a
	// stop's escalation to land in the middle of them. The group id outlives the
	// handles precisely so that escalation reaches what the command left behind
	// instead of being refused.
	if pgid != 0 {
		return signalProcessGroup(pgid, sig)
	}
	return errNoRunningProcess
}

// forceKill sends the SIGKILL that follows a graceful stop which ran out its
// grace period, and latches forceKilled once that SIGKILL reached the run. The
// event saying so is appended by whichever SIGKILL latched first, so a client
// escalating at the same deadline as the monitor adds nothing.
//
// procMu is held across the signal and the latch. The run gives its process
// group up under procMu and only then reads the latch, so a SIGKILL racing the
// end of the run either reached it and shows in what the run reports, or found
// nothing left to reach.
func (m *Monitor) forceKill() error {
	m.procMu.Lock()
	err := signalRun(m.cmd, m.runPgid, syscall.SIGKILL)
	first := err == nil && m.forceKilled.CompareAndSwap(false, true)
	m.procMu.Unlock()
	if first {
		m.emitEvent(model.Event{
			Time: time.Now().UTC(),
			Type: model.EventTypeStopped,
			ID:   m.ID,
			Attrs: map[string]string{
				"signal": strconv.Itoa(int(syscall.SIGKILL)),
				"reason": "timeout",
			},
		})
	}
	return err
}

// StopProcess sends a signal to the running command and prevents restart.
//
// It reports success even when the signal reached nothing. Stopping is about
// the command staying down, and the restart suppression is latched before the
// signal goes out, so a stop landing between one run's end and the next one's
// start - or on a group whose last member has already exited - has done its
// job: the loop ends instead of starting another run. SignalProcess keeps
// returning those errors, because a bare signal that hit nothing is worth
// reporting.
//
// A positive timeout on a signal other than SIGKILL schedules a SIGKILL for
// once it expires. The monitor owns that escalation because the client that
// asked for the stop may be gone before its own timeout: an interrupted or
// crashed CLI would otherwise leave a command that ignores sig running for
// good. Each stop replaces the deadline an earlier one scheduled. The deadline
// is armed before sig goes out, so a command that dies of sig at once cannot
// finish its run ahead of the arming and leave the deadline to outlive it.
//
// A run whose command has a stop command stops differently, unless sig is
// SIGKILL: the stop begins the run's stop sequence and returns without waiting
// for it. The sequence runs the stop command, bounded by timeout, and only
// then sends sig and arms the deadline (see stopSequence). A graceful stop
// that finds the sequence begun changes nothing. SIGKILL takes the path above
// at once in every stage and kills a stop command that is still running.
//
// A SIGKILL that lands while a graceful stop of the run is in progress is that
// stop's escalation, the client's own once its wait is over, and is recorded as
// a forced kill the way the deadline's is (see forceKill).
func (m *Monitor) StopProcess(sig syscall.Signal, timeout time.Duration) error {
	m.stopRequested.Store(true)
	// Latched ahead of the signal for the same reason as the deadline below: a
	// command that dies of it at once must find the run end already knowing.
	if sig == syscall.SIGKILL {
		m.stopKilled.Store(true)
	}

	m.procMu.Lock()
	escalating := sig == syscall.SIGKILL && m.gracefulStop
	if sig != syscall.SIGKILL {
		m.gracefulStop = true
	}
	seq := m.stopSeq
	if seq != nil && sig != syscall.SIGKILL {
		m.beginStopSequence(seq, sig, timeout)
		m.procMu.Unlock()
		return nil
	}
	if m.stopDeadline != nil {
		m.stopDeadline.Stop()
		m.stopDeadline = nil
	}
	if sig != syscall.SIGKILL && timeout > 0 {
		m.stopDeadline = time.AfterFunc(timeout, m.escalateStop)
	}
	m.procMu.Unlock()

	if seq != nil {
		// Only SIGKILL gets here with a sequence, and it does not wait for a stop
		// command still running.
		seq.cancel()
	}

	var err error
	if escalating {
		err = m.forceKill()
	} else {
		err = m.SignalProcess(sig)
	}
	if errors.Is(err, errNoRunningProcess) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// escalateStop is what a stop's deadline runs: SIGKILL to whatever the command
// still has, live child or leftovers alike. Nothing left to signal means the
// stop already worked. Only a graceful stop arms a deadline, so a SIGKILL that
// reached anything is a forced kill.
func (m *Monitor) escalateStop() {
	m.stopKilled.Store(true)
	err := m.forceKill()
	if err == nil || errors.Is(err, errNoRunningProcess) || errors.Is(err, syscall.ESRCH) {
		return
	}
	m.Logger.Warn("escalate stop to SIGKILL", slog.String("error", err.Error()))
}

// GetState returns the current command state.
func (m *Monitor) GetState() (model.EventType, int, int) {
	state, ec, _, _ := m.store.GetCommandState(m.ID)
	exitCode := 0
	if ec != nil {
		exitCode = *ec
	}
	m.procMu.Lock()
	cmd := m.cmd
	m.procMu.Unlock()
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	return state, exitCode, pid
}

func listenMonitorSocket(sockPath string) (net.Listener, error) {
	if sockPath == "" {
		return nil, fmt.Errorf("socket path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		return nil, err
	}
	_ = os.Remove(sockPath)
	return net.Listen("unix", sockPath)
}

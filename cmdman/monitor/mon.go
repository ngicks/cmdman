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

	// procMu guards the per-run process handles below - ptmx, stdin and cmd.
	// runOnce publishes them once the child is wired and clears them once it has
	// been reaped, so an RPC arriving either side of a run sees the whole trio
	// change at once instead of a half-torn-down run. It is never held across a
	// write to the child.
	procMu sync.Mutex
	ptmx   *os.File
	stdin  io.WriteCloser
	cmd    *exec.Cmd
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

	// sweepFn terminates the processes a finished run left behind and reports
	// how many outlived the sweep. It is a field so a test can drive the
	// giving-up path without a process that genuinely refuses to die.
	sweepFn func(ctx context.Context, logger *slog.Logger, pgid int) int

	// stopRequested is set by the Signal RPC to prevent restarts.
	stopRequested atomic.Bool
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
	pid := 0
	if m.cmd != nil && m.cmd.Process != nil {
		pid = m.cmd.Process.Pid
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
	// Anomalies describe the run that reported them, so the run starting here
	// takes over the record from the one before it.
	m.runAnomalies = nil
	m.stateJSON.Warnings = nil
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

// SignalProcess sends a raw signal to the running command and any
// descendants it has spawned within its process group.
func (m *Monitor) SignalProcess(sig syscall.Signal) error {
	m.procMu.Lock()
	cmd := m.cmd
	m.procMu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("no running process")
	}
	return signalProcessGroup(cmd.Process.Pid, sig)
}

// StopProcess sends a signal to the running command and prevents restart.
func (m *Monitor) StopProcess(sig syscall.Signal) error {
	m.stopRequested.Store(true)
	return m.SignalProcess(sig)
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

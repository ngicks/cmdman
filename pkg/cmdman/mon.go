package cmdman

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

	"github.com/creack/pty/v2"
	"google.golang.org/grpc"

	pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/eventlog"
	"github.com/ngicks/cmdman/pkg/cmdman/internal/flock"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	cmdstore "github.com/ngicks/cmdman/pkg/cmdman/store"
)

// Monitor is the per-command monitor process.
type Monitor struct {
	ID         string
	CommandDir string
	DBPath     string
	Config     CmdmanConfig
	Logger     *slog.Logger

	cleanUp []func() error

	store     *cmdstore.Store
	cfg       *model.CommandConfig
	stateJSON *model.CommandState
	evtLog    *eventlog.Writer

	lis net.Listener

	ptmx    *os.File
	stdin   io.WriteCloser
	stdinMu sync.Mutex
	cmd     *exec.Cmd
	ring    *ringBuffer

	outputMu          sync.Mutex
	outputBridge      *broadcaster[logdriver.LogLine]
	stateChangeBridge *broadcaster[monitorStateChange]
	logWriter         logdriver.Writer
	terminalState     *terminalPaneState

	grpcServer *grpc.Server
	sockPath   string

	// wg tracks per-request goroutines spawned by RPC handlers (e.g. the
	// Attach stream-recv pump). RPC handlers that need to spawn a helper
	// goroutine register it here instead of joining inside the handler.
	// The supervisor waits on this group between GracefulStop (which is
	// what unblocks gRPC Recv calls) and resource teardown.
	wg sync.WaitGroup

	// stopRequested is set by the Signal RPC to prevent restarts.
	stopRequested atomic.Bool
}

func newMonitor(
	ctx context.Context,
	id string,
	cfg CmdmanConfig,
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

	return &Monitor{
		ID:                id,
		CommandDir:        commandDir,
		DBPath:            dbPath,
		Config:            cfg,
		Logger:            logger,
		outputBridge:      newBroadcaster[logdriver.LogLine](),
		stateChangeBridge: newBroadcaster[monitorStateChange](),
		terminalState:     newTerminalPaneState(),
		store:             st,
		cfg:               commandCfg,
		evtLog:            evtLog,
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

	if err := flock.TryLockExclusive(f); err != nil {
		return fmt.Errorf("lock pid file %q: %w", pidPath, err)
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
	})
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
	})
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
	if m.stdin == nil {
		return fmt.Errorf("no stdin")
	}
	m.stdinMu.Lock()
	defer m.stdinMu.Unlock()
	if m.stdin == nil {
		return fmt.Errorf("no stdin")
	}
	_, err := m.stdin.Write(data)
	return err
}

// Resize changes the PTY window size.
func (m *Monitor) Resize(rows, cols uint16) error {
	if m.ptmx == nil {
		return fmt.Errorf("no pty")
	}
	return pty.Setsize(m.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// SignalProcess sends a raw signal to the running command and any
// descendants it has spawned within its process group.
func (m *Monitor) SignalProcess(sig syscall.Signal) error {
	if m.cmd == nil || m.cmd.Process == nil {
		return fmt.Errorf("no running process")
	}
	return signalProcessGroup(m.cmd.Process.Pid, sig)
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
	pid := 0
	if m.cmd != nil && m.cmd.Process != nil {
		pid = m.cmd.Process.Pid
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

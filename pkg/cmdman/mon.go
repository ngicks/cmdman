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
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
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
	cfg       *cmdstore.CommandConfigJSON
	stateJSON *cmdstore.CommandStateJSON

	lis net.Listener

	ptmx    *os.File
	stdin   io.WriteCloser
	stdinMu sync.Mutex
	cmd     *exec.Cmd
	ring    *ringBuffer

	outputMu     sync.Mutex
	outputBridge *spmcPipe[logdriver.LogLine]
	logWriter    logdriver.Writer

	grpcServer *grpc.Server
	sockPath   string

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

	return &Monitor{
		ID:           id,
		CommandDir:   commandDir,
		DBPath:       dbPath,
		Config:       cfg,
		Logger:       logger,
		outputBridge: newFanout[logdriver.LogLine](),
		store:        st,
		cfg:          commandCfg,
		ring:         newRingBuffer(commandCfg.ScrollbackBytes),
		stateJSON: &cmdstore.CommandStateJSON{
			MonitorPID: os.Getpid(),
		},
		cleanUp: []func() error{
			st.Close,
		},
	}, nil
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

	if err := flock_trylock(f); err != nil {
		return fmt.Errorf("lock pid file %q: %w", pidPath, err)
	}
	cleanUp = append(
		cleanUp,
		func() error { return flock_unlock(f) },
		func() error { return os.Remove(pidPath) },
	)

	if err := m.store.UpdateCommandState(
		m.ID,
		cmdstore.StateStarting,
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
		cmdstore.StateStarting,
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
	defer m.grpcServer.GracefulStop()

	// Handle SIGTERM for graceful shutdown.
	sigCtx, sigStop := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer sigStop()

	return m.runLoop(sigCtx)
}

func (m *Monitor) setRunning() {
	m.stateJSON.StartedAt = time.Now().UTC().Format(time.RFC3339)
	if err := m.store.UpdateCommandState(
		m.ID,
		cmdstore.StateRunning,
		nil,
		m.stateJSON,
	); err != nil {
		m.Logger.Error("update state to running failed", slog.String("error", err.Error()))
	}
}

func (m *Monitor) setExited(exitCode int) {
	_ = m.store.UpdateCommandState(m.ID, cmdstore.StateExited, &exitCode, m.stateJSON)
}

func (m *Monitor) setFailed(errMsg string) {
	m.stateJSON.Error = errMsg
	_ = m.store.UpdateCommandState(m.ID, cmdstore.StateFailed, nil, m.stateJSON)
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

// SignalProcess sends a raw signal to the running command.
func (m *Monitor) SignalProcess(sig syscall.Signal) error {
	if m.cmd == nil || m.cmd.Process == nil {
		return fmt.Errorf("no running process")
	}
	return m.cmd.Process.Signal(sig)
}

// StopProcess sends a signal to the running command and prevents restart.
func (m *Monitor) StopProcess(sig syscall.Signal) error {
	m.stopRequested.Store(true)
	return m.SignalProcess(sig)
}

// GetState returns the current command state.
func (m *Monitor) GetState() (string, int, int) {
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

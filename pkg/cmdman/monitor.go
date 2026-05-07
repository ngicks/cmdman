package cmdman

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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

	store     *cmdstore.Store
	cfg       *cmdstore.CommandConfigJSON
	stateJSON *cmdstore.CommandStateJSON

	ptmx    *os.File
	cmd     *exec.Cmd
	fanout  *fanout
	ring    *ringBuffer
	stdinCh chan []byte

	grpcServer *grpc.Server
	sockPath   string

	// stopRequested is set by the Signal RPC to prevent restarts.
	stopRequested atomic.Bool
}

// RunMonitor is the main entry point for the monitor process.
// It reads config, starts the command, and serves gRPC until the command exits.
func RunMonitor(ctx context.Context, id string, cfg CmdmanConfig, logger *slog.Logger) error {
	commandDir, err := cfg.CommandDir(id)
	if err != nil {
		return err
	}
	dbPath, err := cfg.DBPath()
	if err != nil {
		return err
	}

	m := &Monitor{
		ID:         id,
		CommandDir: commandDir,
		DBPath:     dbPath,
		Config:     cfg,
		Logger:     logger,
		fanout:     newFanout(),
		stdinCh:    make(chan []byte, 64),
	}

	st, err := cmdstore.OpenStore(dbPath, true)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	m.store = st

	commandCfg, err := cmdstore.ReadCommandConfig(commandDir)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	m.cfg = commandCfg
	m.ring = newRingBuffer(commandCfg.ScrollbackBytes)
	m.stateJSON = &cmdstore.CommandStateJSON{}

	// Update state to starting.
	m.stateJSON.MonitorPID = os.Getpid()
	if err := m.store.UpdateCommandState(
		m.ID,
		cmdstore.StateStarting,
		nil,
		m.stateJSON,
	); err != nil {
		return fmt.Errorf("update state to starting: %w", err)
	}

	// Create runtime directory and PID file.
	runtimeDir, err := m.Config.MonitorRuntimeDir(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}
	pidPath, err := m.Config.MonitorPIDPath(id)
	if err != nil {
		return err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer os.Remove(pidPath)

	// Start gRPC server.
	m.sockPath, err = m.Config.MonitorSocketPath(id)
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

	lis, err := listenMonitorSocket(m.sockPath)
	if err != nil {
		return fmt.Errorf("listen socket: %w", err)
	}
	defer os.Remove(m.sockPath)
	defer lis.Close()

	m.grpcServer = grpc.NewServer()
	pb.RegisterCommandMonitorServiceServer(m.grpcServer, &monitorServer{monitor: m})

	go func() {
		if err := m.grpcServer.Serve(lis); err != nil {
			m.Logger.Error("grpc serve error", slog.String("error", err.Error()))
		}
	}()
	defer m.grpcServer.GracefulStop()

	// Handle SIGTERM for graceful shutdown.
	sigCtx, sigStop := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer sigStop()

	return m.runLoop(sigCtx)
}

func (m *Monitor) runLoop(ctx context.Context) error {
	for {
		// Re-read config on each restart iteration.
		cfg, err := cmdstore.ReadCommandConfig(m.CommandDir)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		m.cfg = cfg

		exitCode, err := m.runOnce(ctx)
		if err != nil {
			// If context was cancelled, treat as graceful stop.
			if ctx.Err() != nil {
				m.setExited(-1)
				return nil
			}
			m.setFailed(fmt.Sprintf("run failed: %v", err))
			return err
		}

		// Record exit.
		ec := exitCode
		m.stateJSON.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = m.store.InsertCommandExitCode(m.ID, exitCode)

		// Check restart policy.
		switch m.cfg.RestartPolicy {
		case cmdstore.RestartPolicyNo:
			m.setExited(ec)
			return m.maybeAutoRemove()
		case cmdstore.RestartPolicyOnFailure:
			if exitCode == 0 {
				m.setExited(ec)
				return m.maybeAutoRemove()
			}
		case cmdstore.RestartPolicyAlways:
			// Continue loop unless context cancelled.
		default:
			m.setExited(ec)
			return m.maybeAutoRemove()
		}

		// Check if stop was requested or context was cancelled.
		if m.stopRequested.Load() {
			m.setExited(ec)
			return m.maybeAutoRemove()
		}
		select {
		case <-ctx.Done():
			m.setExited(ec)
			return m.maybeAutoRemove()
		default:
		}

		m.stateJSON.RestartCount++
		m.Logger.Info("restarting command", slog.Int("restart_count", m.stateJSON.RestartCount))
	}
}

func (m *Monitor) runOnce(ctx context.Context) (int, error) {
	cmd := exec.CommandContext(ctx, m.cfg.Argv[0], m.cfg.Argv[1:]...)
	cmd.Dir = m.cfg.Dir
	cmd.Env = m.cfg.Env
	if len(cmd.Env) == 0 {
		return -1, fmt.Errorf("command config env is empty")
	}
	// Cancel via signal, not process kill.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	opts := make(logdriver.Options, len(m.cfg.LogOpts)+1)
	maps.Copy(opts, m.cfg.LogOpts)
	opts[cmdstore.LogOptPath] = m.cfg.LogPath()
	logWriter, err := logdriver.New(m.cfg.LogDriver, opts)
	if err != nil {
		return -1, fmt.Errorf("open log writer: %w", err)
	}
	stdoutLogWriter := logdriver.NewStreamWriter(logWriter, logdriver.StreamStdout)
	defer func() {
		if cerr := logWriter.Close(); cerr != nil {
			m.Logger.Warn("close log writer", slog.String("error", cerr.Error()))
		}
	}()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return -1, fmt.Errorf("pty start: %w", err)
	}
	m.ptmx = ptmx
	m.cmd = cmd

	// Update state to running.
	m.stateJSON.StartedAt = time.Now().UTC().Format(time.RFC3339)
	if err := m.store.UpdateCommandState(
		m.ID,
		cmdstore.StateRunning,
		nil,
		m.stateJSON,
	); err != nil {
		m.Logger.Error("update state to running failed", slog.String("error", err.Error()))
	}

	// PTY read goroutine: read -> ring buffer + log file + fanout.
	var wg sync.WaitGroup
	wg.Go(func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				data := buf[:n]
				m.ring.Write(data)
				if _, werr := stdoutLogWriter.Write(data); werr != nil {
					m.Logger.Warn("log writer", slog.String("error", werr.Error()))
				}
				m.fanout.Send(data)
			}
			if err != nil {
				return
			}
		}
	})

	// PTY write goroutine: stdin channel -> PTY.
	done := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case data := <-m.stdinCh:
				if _, err := ptmx.Write(data); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	})

	// Wait for command exit.
	err = cmd.Wait()
	ptmx.Close()
	m.ptmx = nil
	m.cmd = nil

	close(done)
	wg.Wait()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func (m *Monitor) setExited(exitCode int) {
	ec := exitCode
	_ = m.store.UpdateCommandState(m.ID, cmdstore.StateExited, &ec, m.stateJSON)
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

// QueueStdin sends data to the monitor's stdin channel for the PTY.
func (m *Monitor) QueueStdin(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if m.ptmx == nil {
		return fmt.Errorf("no pty")
	}
	select {
	case m.stdinCh <- data:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

// CheckMonitorAlive checks if a monitor process is still alive by PID.
func CheckMonitorAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// CleanStaleEntries checks for stale monitors and marks them as failed.
func CleanStaleEntries(st *cmdstore.Store, cfg CmdmanConfig) error {
	entries, err := st.ListCommands(true, nil)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.State != cmdstore.StateStarting && e.State != cmdstore.StateRunning {
			continue
		}
		if e.StateJSON.MonitorPID > 0 && !CheckMonitorAlive(e.StateJSON.MonitorPID) {
			e.StateJSON.Error = "monitor died unexpectedly"
			_ = st.UpdateCommandState(e.ID, cmdstore.StateFailed, nil, e.StateJSON)

			// Auto-remove if requested.
			if e.ConfigJSON.Annotations[cmdstore.AnnotationAutoRemove] == "true" {
				_ = st.DeleteCommand(e.ID)
				_ = os.RemoveAll(e.ConfigJSON.CommandDir)
				runtimeDir, err := cfg.MonitorRuntimeDir(e.ID)
				if err == nil {
					_ = os.RemoveAll(runtimeDir)
				}
			}
		}
	}
	return nil
}

// listenMonitorSocket is already defined above, but we need a helper for the gRPC part.
// Using the existing listenUnixDomainSocket pattern from pkg/cmdman/server.go.
var _ io.Closer = (*Monitor)(nil) // ensure Monitor satisfies io.Closer if needed

// Close is not needed as cleanup happens in RunMonitor defers.
func (m *Monitor) Close() error { return nil }

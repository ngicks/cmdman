package cmdman

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty/v2"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	cmdstore "github.com/ngicks/cmdman/pkg/cmdman/store"
)

func (m *Monitor) runLoop(ctx context.Context) (err error) {
	org := m.stateJSON.RestartCount
	for ; ; m.stateJSON.RestartCount++ {
		if m.stateJSON.RestartCount > org {
			m.Logger.Info("restarting command", slog.Int("restart_count", m.stateJSON.RestartCount))
		}

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
		m.stateJSON.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		_ = m.store.InsertCommandExitCode(m.ID, exitCode)

		// Check restart policy.
		switch m.cfg.RestartPolicy {
		case cmdstore.RestartPolicyNo:
		case cmdstore.RestartPolicyOnFailure:
			if exitCode != 0 && !m.stopRequested.Load() && ctx.Err() == nil {
				continue
			}
		case cmdstore.RestartPolicyAlways:
			if !m.stopRequested.Load() && ctx.Err() == nil {
				continue
			}
		}

		m.setExited(exitCode)
		return m.maybeAutoRemove()
	}
}

func (m *Monitor) wireUpCmd(ctx context.Context) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, m.cfg.Argv[0], m.cfg.Argv[1:]...)
	cmd.Dir = m.cfg.Dir
	cmd.Env = m.cfg.Env
	if len(cmd.Env) == 0 {
		return nil, fmt.Errorf("command config env is empty")
	}
	// Cancel via signal, not process kill.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{}

	return cmd, nil
}

func (m *Monitor) logWriter() (logdriver.Writer, error) {
	opts := make(logdriver.Options, len(m.cfg.LogOpts)+1)
	maps.Copy(opts, m.cfg.LogOpts)
	opts[cmdstore.LogOptPath] = m.cfg.LogPath()
	logWriter, err := logdriver.New(m.cfg.LogDriver, opts)
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

	logWriter, err := m.logWriter()
	if err != nil {
		return -1, err
	}

	defer func() {
		if cerr := logWriter.Close(); cerr != nil {
			m.Logger.Warn("close log writer", slog.String("error", cerr.Error()))
		}
	}()

	var waitFn func()
	if m.cfg.Tty {
		waitFn, err = m.pipeIoTty(cmd, logWriter)
	} else {
		waitFn, err = m.pipeIoPipe(cmd, logWriter)
	}

	if err != nil {
		return -1, err
	}

	m.cmd = cmd

	m.setRunning()

	// Wait for command exit.
	err = cmd.Wait()
	m.ptmx = nil
	m.stdin = nil
	m.cmd = nil

	waitFn()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func (m *Monitor) pipeIoTty(cmd *exec.Cmd, logWriter logdriver.Writer) (func(), error) {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}
	m.ptmx = ptmx
	m.stdin = ptmx

	var wg sync.WaitGroup

	stdoutLogWriter := logdriver.NewStreamWriter(logWriter, logdriver.StreamStdout)

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

	return func() {
		_ = ptmx.Close()
		wg.Wait()
	}, nil
}

func (m *Monitor) pipeIoPipe(cmd *exec.Cmd, logWriter logdriver.Writer) (func(), error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	m.stdin = stdin

	lockedLogWriter := &lockedLogWriter{w: logWriter}
	cmd.Stdout = &monitorOutputWriter{
		monitor: m,
		log:     logdriver.NewStreamWriter(lockedLogWriter, logdriver.StreamStdout),
	}
	cmd.Stderr = &monitorOutputWriter{
		monitor: m,
		log:     logdriver.NewStreamWriter(lockedLogWriter, logdriver.StreamStderr),
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}
	m.stdin = stdin
	m.cmd = cmd

	return func() {}, nil
}

type monitorOutputWriter struct {
	monitor *Monitor
	log     io.Writer
}

func (w *monitorOutputWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	w.monitor.outputMu.Lock()
	defer w.monitor.outputMu.Unlock()

	w.monitor.ring.Write(data)
	if _, err := w.log.Write(data); err != nil {
		w.monitor.Logger.Warn("log writer", slog.String("error", err.Error()))
	}
	w.monitor.fanout.Send(data)
	return len(data), nil
}

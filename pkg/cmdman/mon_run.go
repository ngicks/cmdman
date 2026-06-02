package cmdman

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os/exec"
	"sync"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	cmdstore "github.com/ngicks/cmdman/pkg/cmdman/store"
)

// RunMonitor is the main entry point for the monitor process.
// It reads config, starts the command, and serves gRPC until the command exits.
func RunMonitor(ctx context.Context, id string, cfg CmdmanConfig, logger *slog.Logger) error {
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
			return fmt.Errorf("read config: %w", err)
		}
		m.cfg = cfg

		exitCode, err := m.runOnce(ctx)
		if err != nil {
			// If context was cancelled, treat as graceful stop.
			if ctx.Err() != nil {
				m.outputBridge.Close()
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
	cmd.Env = withCommandContextEnv(m.cfg.Env, m.Config, m.ID, m.cfg.CommandDir)
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
	m.terminalState.reset()
	m.outputMu.Unlock()
	defer func() {
		m.outputMu.Lock()
		if m.logWriter == logWriter {
			m.logWriter = nil
		}
		m.outputMu.Unlock()
	}()

	var waitFn func()
	if m.cfg.Tty {
		waitFn, err = m.writeTty(cmd)
	} else {
		waitFn, err = m.wirePipe(cmd)
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

func (m *Monitor) writeTty(cmd *exec.Cmd) (func(), error) {
	ptmx, err := startTty(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}
	m.ptmx = ptmx
	m.stdin = ptmx

	var wg sync.WaitGroup

	wg.Go(func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				m.logCommandOutput(logdriver.StreamStdout, data)
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

func (m *Monitor) wirePipe(cmd *exec.Cmd) (func(), error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	m.stdin = stdin

	cmd.Stdout = &monitorOutputWriter{
		monitor: m,
		stream:  logdriver.StreamStdout,
	}
	cmd.Stderr = &monitorOutputWriter{
		monitor: m,
		stream:  logdriver.StreamStderr,
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	m.stdin = stdin
	m.cmd = cmd

	return func() {}, nil
}

func (m *Monitor) logCommandOutput(stream logdriver.Stream, data []byte) {
	if len(data) == 0 {
		return
	}
	lines := logdriver.SplitLogLines(time.Now(), stream, data)
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
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
}

type monitorOutputWriter struct {
	monitor *Monitor
	stream  logdriver.Stream
}

func (w *monitorOutputWriter) Write(data []byte) (int, error) {
	buf := make([]byte, len(data))
	copy(buf, data)
	w.monitor.logCommandOutput(w.stream, buf)
	return len(data), nil
}

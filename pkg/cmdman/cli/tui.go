package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/tui"
)

// RunTUI runs the interactive TUI directly in the current terminal.
func RunTUI(ctx context.Context, svc *cmdman.Service) error {
	return tui.Run(ctx, tui.Options{
		Backend:   tui.NewServiceBackend(svc),
		Version:   cmdman.Version,
		AltScreen: true,
	})
}

// RunTUIChild runs the TUI inside a multiplexer popup, reporting startup and
// final status to the launcher over the IPC endpoint at ipcPath. It is the
// implementation of the hidden `cmdman tui __child` subcommand.
func RunTUIChild(ctx context.Context, svc *cmdman.Service, ipcPath string) error {
	var enc *json.Encoder
	if ipcPath != "" {
		if conn, err := net.Dial("unix", ipcPath); err == nil {
			defer conn.Close()
			enc = json.NewEncoder(conn)
		}
	}
	send := func(m ipcMessage) {
		if enc != nil {
			_ = enc.Encode(m)
		}
	}
	send(ipcMessage{Kind: ipcStarted})
	err := tui.Run(ctx, tui.Options{
		Backend:   tui.NewServiceBackend(svc),
		Version:   cmdman.Version,
		AltScreen: true,
	})
	if err != nil {
		send(ipcMessage{Kind: ipcError, Error: err.Error()})
		return err
	}
	send(ipcMessage{Kind: ipcDone})
	return nil
}

// PopupConfig describes how to launch the TUI as a multiplexer popup.
type PopupConfig struct {
	// Driver is the raw --popup value ("", "true", "tmux", or "zellij").
	// Empty or "true" means infer from the environment.
	Driver string
	// Env is the environment used for driver inference and forwarded to the
	// popup process. Defaults to os.Environ() when nil.
	Env []string
	// Cwd is the working directory forwarded to the popup so active-project
	// detection matches direct mode.
	Cwd string
	// Executable is the path to the cmdman binary launched inside the popup.
	Executable string
	// DataDir and RuntimeDir are forwarded so the popup uses the same store and
	// runtime targets as the launcher. Empty values are not forwarded.
	DataDir    string
	RuntimeDir string
	// ConfPath is the $CMDMAN_CONF value forwarded to the popup. Empty is not
	// forwarded.
	ConfPath string
}

// LaunchTUIPopup gathers the launcher's process context (executable path,
// working directory, config file) and starts the popup. It is the entry point
// the cobra command calls; gathering process/env state here keeps ./cmd thin.
func LaunchTUIPopup(ctx context.Context, driverValue, dataDir, runtimeDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("tui: locate executable: %w", err)
	}
	cwd, _ := os.Getwd()
	return RunTUIPopup(ctx, PopupConfig{
		Driver:     driverValue,
		Cwd:        cwd,
		Executable: exe,
		DataDir:    dataDir,
		RuntimeDir: runtimeDir,
		ConfPath:   os.Getenv("CMDMAN_CONF"),
	})
}

// RunTUIPopup is the `cmdman tui --popup` launcher. It resolves the popup
// driver, opens a multiplexer popup running `cmdman tui __child`, waits for the
// child's final status over a Unix-socket IPC channel, and returns the child's
// result.
func RunTUIPopup(ctx context.Context, cfg PopupConfig) error {
	env := cfg.Env
	if env == nil {
		env = os.Environ()
	}
	driver, err := resolvePopupDriver(cfg.Driver, env)
	if err != nil {
		return err
	}
	switch driver {
	case "tmux":
		return runTmuxPopup(ctx, cfg, env)
	default:
		return fmt.Errorf("tui: popup driver %q is not implemented yet (v1 ships tmux only)", driver)
	}
}

// resolvePopupDriver selects the popup backend. Bare/empty values infer from
// the environment; zellij is accepted by inference/selection only to report
// that it is not implemented in v1.
func resolvePopupDriver(value string, env []string) (string, error) {
	driver := value
	if driver == "" || driver == "true" {
		driver = inferMuxDriver(env)
	}
	switch driver {
	case "tmux":
		return "tmux", nil
	case "zellij":
		return "", errors.New("tui: --popup=zellij is not implemented yet (v1 ships tmux only)")
	default:
		return "", fmt.Errorf("tui: unknown popup driver %q", driver)
	}
}

// inferMuxDriver mirrors mux driver inference: prefer an active tmux server,
// then zellij, then fall back to tmux.
func inferMuxDriver(env []string) string {
	if envOf(env, "TMUX") != "" {
		return "tmux"
	}
	if envOf(env, "ZELLIJ") != "" {
		return "zellij"
	}
	return "tmux"
}

func envOf(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):]
		}
	}
	return ""
}

// childCommand builds the argv for the popup child process.
func (cfg PopupConfig) childCommand(ipcPath string) []string {
	args := []string{cfg.Executable, "tui", "__child", "--ipc", ipcPath}
	if cfg.DataDir != "" {
		args = append(args, "--data-dir", cfg.DataDir)
	}
	if cfg.RuntimeDir != "" {
		args = append(args, "--runtime-dir", cfg.RuntimeDir)
	}
	return args
}

func runTmuxPopup(ctx context.Context, cfg PopupConfig, env []string) error {
	ipcPath, ln, cleanup, err := newIPCEndpoint()
	if err != nil {
		return err
	}
	defer cleanup()

	cmdStr := shellJoin(cfg.childCommand(ipcPath))
	args := []string{"display-popup", "-E"}
	if cfg.Cwd != "" {
		args = append(args, "-d", cfg.Cwd)
	}
	args = append(args, cmdStr)

	cmd := exec.CommandContext(ctx, "tmux", args...)
	cmd.Env = env
	if cfg.ConfPath != "" {
		cmd.Env = append(cmd.Env, "CMDMAN_CONF="+cfg.ConfPath)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	ipcResult := make(chan error, 1)
	go func() { ipcResult <- waitForChild(ln) }()

	tmuxErr := cmd.Run()
	// Unblock the IPC accept if the child never connected.
	_ = ln.Close()
	childErr := <-ipcResult

	if childErr != nil {
		return childErr
	}
	if tmuxErr != nil {
		return fmt.Errorf("tui: tmux popup failed: %w", tmuxErr)
	}
	return nil
}

// ipcMessage is the small launcher<->child control payload. Normal rendered UI
// never travels over this channel.
type ipcMessage struct {
	Kind  string `json:"kind"`
	Error string `json:"error,omitempty"`
}

const (
	ipcStarted = "started"
	ipcDone    = "done"
	ipcError   = "error"
)

// newIPCEndpoint creates a user-only Unix-domain socket for popup launcher IPC.
func newIPCEndpoint() (path string, ln net.Listener, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "cmdman-tui-")
	if err != nil {
		return "", nil, nil, fmt.Errorf("tui: create ipc dir: %w", err)
	}
	sockPath := filepath.Join(dir, "ipc.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, nil, fmt.Errorf("tui: listen ipc: %w", err)
	}
	_ = os.Chmod(sockPath, 0o600)
	cleanup = func() {
		_ = l.Close()
		_ = os.RemoveAll(dir)
	}
	return sockPath, l, cleanup, nil
}

// waitForChild accepts the child connection and reads control messages until
// the connection closes, returning any reported error.
func waitForChild(ln net.Listener) error {
	conn, err := ln.Accept()
	if err != nil {
		return nil // listener closed before the child connected
	}
	defer conn.Close()
	dec := json.NewDecoder(conn)
	var finalErr error
	for {
		var m ipcMessage
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m.Kind == ipcError {
			finalErr = errors.New(m.Error)
		}
	}
	return finalErr
}

// shellJoin quotes argv into a single POSIX shell command string.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`*?[]{}()<>|&;#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

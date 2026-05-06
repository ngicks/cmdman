package cmdman

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

const (
	ENV_CMDMAN_DATA_DIR    = "CMDMAN_DATA_DIR"
	ENV_CMDMAN_RUNTIME_DIR = "CMDMAN_RUNTIME_DIR"
)

// CmdmanConfig contains process-wide configuration and default expansion rules.
type CmdmanConfig struct {
	DataDir                string
	RuntimeDir             string
	DefaultWorkingDir      string
	DefaultEnvironment     []string
	DefaultScrollbackBytes int
}

// WithDefaults fills empty fields from the environment and validates the result.
func (c CmdmanConfig) WithDefaults() (CmdmanConfig, error) {
	if c.DataDir == "" {
		dir, err := defaultDataDir()
		if err != nil {
			return CmdmanConfig{}, err
		}
		c.DataDir = dir
	}
	if c.RuntimeDir == "" {
		dir, err := defaultRuntimeDir()
		if err != nil {
			return CmdmanConfig{}, err
		}
		c.RuntimeDir = dir
	}
	if c.DefaultWorkingDir == "" {
		dir, err := os.Getwd()
		if err != nil {
			return CmdmanConfig{}, fmt.Errorf("get working directory: %w", err)
		}
		c.DefaultWorkingDir = dir
	}
	if len(c.DefaultEnvironment) == 0 {
		c.DefaultEnvironment = append([]string(nil), os.Environ()...)
	}
	if c.DefaultScrollbackBytes == 0 {
		c.DefaultScrollbackBytes = store.DefaultScrollbackBytes
	}
	if err := c.Validate(); err != nil {
		return CmdmanConfig{}, err
	}
	return c, nil
}

// Validate ensures all required fields are explicitly populated.
func (c CmdmanConfig) Validate() error {
	switch {
	case c.DataDir == "":
		return errors.New("cmdman config: data dir is empty")
	case c.RuntimeDir == "":
		return errors.New("cmdman config: runtime dir is empty")
	case c.DefaultWorkingDir == "":
		return errors.New("cmdman config: default working dir is empty")
	case len(c.DefaultEnvironment) == 0:
		return errors.New("cmdman config: default environment is empty")
	case c.DefaultScrollbackBytes <= 0:
		return fmt.Errorf("cmdman config: default scrollback bytes must be positive: %d", c.DefaultScrollbackBytes)
	}
	return nil
}

// DBPath returns the configured SQLite database path.
func (c CmdmanConfig) DBPath() (string, error) {
	if c.DataDir == "" {
		return "", errors.New("cmdman config: data dir is empty")
	}
	return filepath.Join(c.DataDir, "commands.db"), nil
}

// CommandDir returns the configured per-command directory.
func (c CmdmanConfig) CommandDir(id string) (string, error) {
	if c.DataDir == "" {
		return "", errors.New("cmdman config: data dir is empty")
	}
	if id == "" {
		return "", errors.New("cmdman config: command id is empty")
	}
	return filepath.Join(c.DataDir, "commands", id), nil
}

// MonitorRuntimeDir returns the configured per-command runtime directory.
func (c CmdmanConfig) MonitorRuntimeDir(id string) (string, error) {
	if c.RuntimeDir == "" {
		return "", errors.New("cmdman config: runtime dir is empty")
	}
	if id == "" {
		return "", errors.New("cmdman config: command id is empty")
	}
	return filepath.Join(c.RuntimeDir, "cmd", id), nil
}

// MonitorSocketPath returns the configured Unix socket path for a monitor.
func (c CmdmanConfig) MonitorSocketPath(id string) (string, error) {
	runtimeDir, err := c.MonitorRuntimeDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, "monitor.sock"), nil
}

// MonitorPIDPath returns the configured PID file path for a monitor.
func (c CmdmanConfig) MonitorPIDPath(id string) (string, error) {
	runtimeDir, err := c.MonitorRuntimeDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, "pid"), nil
}

func defaultDataDir() (string, error) {
	if dir := os.Getenv(ENV_CMDMAN_DATA_DIR); dir != "" {
		return filepath.Join(dir), nil
	}
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if home == "" {
			return "", errors.New("resolve home directory: empty result")
		}
		dir = filepath.Join(home, ".local", "share")
	}
	if dir == "" {
		return "", errors.New("resolve data dir: empty result")
	}
	return filepath.Join(dir, "cmdman"), nil
}

func defaultRuntimeDir() (string, error) {
	if dir := os.Getenv(ENV_CMDMAN_RUNTIME_DIR); dir != "" {
		return filepath.Join(dir), nil
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = "/tmp"
	}
	if dir == "" {
		return "", errors.New("resolve runtime dir: empty result")
	}
	return filepath.Join(dir, "cmdman"), nil
}

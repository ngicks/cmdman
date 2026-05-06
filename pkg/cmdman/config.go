package cmdman

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

const (
	ENV_CMDMAN_DATA_DIR    = "CMDMAN_DATA_DIR"
	ENV_CMDMAN_RUNTIME_DIR = "CMDMAN_RUNTIME_DIR"
	// ENV_CMDMAN_CONF overrides the config-file path. When unset, the file
	// at ${XDG_CONFIG_HOME:-$HOME/.config}/cmdman/config.json is consulted
	// if it exists.
	ENV_CMDMAN_CONF = "CMDMAN_CONF"
)

// CmdmanConfig contains process-wide configuration and default expansion
// rules. Field values flow in from (highest precedence first): explicit
// caller assignment / Cobra flags, environment variables, the on-disk
// config file, and finally built-in defaults.
type CmdmanConfig struct {
	DataDir                string          `json:"dataDir,omitzero"`
	RuntimeDir             string          `json:"runtimeDir,omitzero"`
	DefaultWorkingDir      string          `json:"defaultWorkingDir,omitzero"`
	DefaultEnvironment     []string        `json:"-"`
	DefaultScrollbackBytes int             `json:"defaultScrollbackBytes,omitzero"`
	DefaultLogDriver       store.LogDriver `json:"defaultLogDriver,omitzero"`
}

// WithDefaults fills empty fields using the configured precedence and
// validates the result.
func (c CmdmanConfig) WithDefaults() (CmdmanConfig, error) {
	fileCfg, err := loadConfigFile()
	if err != nil {
		return CmdmanConfig{}, err
	}

	if c.DataDir == "" {
		c.DataDir = os.Getenv(ENV_CMDMAN_DATA_DIR)
	}
	if c.DataDir == "" {
		c.DataDir = fileCfg.DataDir
	}
	if c.DataDir == "" {
		dir, err := computeDefaultDataDir()
		if err != nil {
			return CmdmanConfig{}, err
		}
		c.DataDir = dir
	}

	if c.RuntimeDir == "" {
		c.RuntimeDir = os.Getenv(ENV_CMDMAN_RUNTIME_DIR)
	}
	if c.RuntimeDir == "" {
		c.RuntimeDir = fileCfg.RuntimeDir
	}
	if c.RuntimeDir == "" {
		dir, err := computeDefaultRuntimeDir()
		if err != nil {
			return CmdmanConfig{}, err
		}
		c.RuntimeDir = dir
	}

	if c.DefaultWorkingDir == "" {
		c.DefaultWorkingDir = fileCfg.DefaultWorkingDir
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
		c.DefaultScrollbackBytes = fileCfg.DefaultScrollbackBytes
	}
	if c.DefaultScrollbackBytes == 0 {
		c.DefaultScrollbackBytes = store.DefaultScrollbackBytes
	}

	if c.DefaultLogDriver == "" {
		c.DefaultLogDriver = fileCfg.DefaultLogDriver
	}
	if c.DefaultLogDriver == "" {
		c.DefaultLogDriver = store.DefaultLogDriver
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
		return fmt.Errorf(
			"cmdman config: default scrollback bytes must be positive: %d",
			c.DefaultScrollbackBytes,
		)
	case !store.IsLogDriver(string(c.DefaultLogDriver)):
		return fmt.Errorf(
			"cmdman config: invalid default log driver %q",
			c.DefaultLogDriver,
		)
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

// loadConfigFile reads the optional JSON config file. Returns an empty
// CmdmanConfig when the file does not exist; returns an error when the
// file exists but cannot be read or parsed.
//
// Path resolution: $CMDMAN_CONF if set, otherwise
// ${XDG_CONFIG_HOME:-$HOME/.config}/cmdman/config.json.
func loadConfigFile() (CmdmanConfig, error) {
	path, err := configFilePath()
	if err != nil {
		return CmdmanConfig{}, err
	}
	if path == "" {
		return CmdmanConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CmdmanConfig{}, nil
		}
		return CmdmanConfig{}, fmt.Errorf("read config file %q: %w", path, err)
	}
	var cfg CmdmanConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return CmdmanConfig{}, fmt.Errorf("parse config file %q: %w", path, err)
	}
	return cfg, nil
}

// configFilePath resolves the on-disk config file path, or returns an
// empty string when no path can be determined (e.g. no $HOME).
func configFilePath() (string, error) {
	if path := os.Getenv(ENV_CMDMAN_CONF); path != "" {
		return path, nil
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// Without HOME there is nowhere to look — treat as "no
			// config file" rather than failing the run.
			return "", nil
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "cmdman", "config.json"), nil
}

func computeDefaultDataDir() (string, error) {
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
	return filepath.Join(dir, "cmdman"), nil
}

func computeDefaultRuntimeDir() (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, "cmdman"), nil
}

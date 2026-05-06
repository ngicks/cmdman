package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigFileName is the fixed name of the per-command configuration file.
const ConfigFileName = "config.json"

// RestartPolicy determines how the monitor handles command exits.
type RestartPolicy string

const (
	RestartPolicyNo        RestartPolicy = "no"
	RestartPolicyOnFailure RestartPolicy = "on-failure"
	RestartPolicyAlways    RestartPolicy = "always"
)

// IsRestartPolicy reports whether s is a valid RestartPolicy value.
func IsRestartPolicy(s string) bool {
	switch RestartPolicy(s) {
	case RestartPolicyNo, RestartPolicyOnFailure, RestartPolicyAlways:
		return true
	}
	return false
}

// CommandConfigJSON is the canonical command configuration stored in CommandConfig.JSON.
type CommandConfigJSON struct {
	// Argv is the command and its arguments.
	Argv []string `json:"argv"`
	// Dir is the working directory for the command.
	Dir string `json:"dir,omitempty"`
	// Env is environment variables for the command.
	Env []string `json:"env,omitempty"`
	// RestartPolicy is one of "no", "on-failure", "always".
	RestartPolicy RestartPolicy `json:"restart_policy"`
	// StopSignal is the default signal used by stop when no override is provided.
	StopSignal string `json:"stop_signal,omitempty"`
	// ScrollbackBytes is the scrollback buffer size in bytes.
	ScrollbackBytes int `json:"scrollback_bytes"`
	// Labels are user-defined key-value metadata.
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations are system metadata (e.g., auto-remove).
	Annotations map[string]string `json:"annotations,omitempty"`
	// CommandDir is the per-command directory path.
	CommandDir string `json:"command_dir"`
}

// ConfigPath returns the full path to this command's config file.
func (c *CommandConfigJSON) ConfigPath() string {
	return filepath.Join(c.CommandDir, ConfigFileName)
}

// Validate rejects incomplete command configs so runtime code can assume values are present.
func (c *CommandConfigJSON) Validate() error {
	if err := c.ValidateCreate(); err != nil {
		return err
	}
	if c.CommandDir == "" {
		return errors.New("command config: command_dir is empty")
	}
	return nil
}

// ValidateCreate validates caller-supplied config before derived fields are filled in.
func (c *CommandConfigJSON) ValidateCreate() error {
	switch {
	case c == nil:
		return errors.New("command config is nil")
	case len(c.Argv) == 0:
		return errors.New("command config: argv is empty")
	case c.Dir == "":
		return errors.New("command config: dir is empty")
	case len(c.Env) == 0:
		return errors.New("command config: env is empty")
	case !IsRestartPolicy(string(c.RestartPolicy)):
		return fmt.Errorf("command config: invalid restart policy %q", c.RestartPolicy)
	case c.StopSignal != "":
		if _, _, err := ParseSignal(c.StopSignal); err != nil {
			return fmt.Errorf("command config: invalid stop_signal %q: %w", c.StopSignal, err)
		}
	case c.ScrollbackBytes <= 0:
		return fmt.Errorf(
			"command config: scrollback_bytes must be positive: %d",
			c.ScrollbackBytes,
		)
	}
	return nil
}

// Write materializes the config as a JSON file in the command directory.
// It creates the directory if it does not exist.
func (c *CommandConfigJSON) Write() error {
	return WriteCommandConfig(c.CommandDir, c)
}

// WriteCommandConfig writes cfg to commandDir/ConfigFileName.
// It creates the directory if it does not exist.
func WriteCommandConfig(commandDir string, cfg *CommandConfigJSON) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(commandDir, ConfigFileName), data, 0o644)
}

// ReadCommandConfig reads ConfigFileName from the given command directory.
func ReadCommandConfig(commandDir string) (*CommandConfigJSON, error) {
	data, err := os.ReadFile(filepath.Join(commandDir, ConfigFileName))
	if err != nil {
		return nil, err
	}
	var cfg CommandConfigJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

const (
	AnnotationAutoRemove = "cmdman.auto-remove"
)

const DefaultScrollbackBytes = 1048576 // 1 MiB

// CommandStateJSON stores mutable runtime fields in CommandState.JSON.
type CommandStateJSON struct {
	// MonitorPID is the PID of the monitor process.
	MonitorPID int `json:"monitor_pid,omitempty"`
	// SocketPath is the Unix socket path for the monitor gRPC server.
	SocketPath string `json:"socket_path,omitempty"`
	// StartedAt is the RFC3339 timestamp when the command started.
	StartedAt string `json:"started_at,omitempty"`
	// FinishedAt is the RFC3339 timestamp when the command finished.
	FinishedAt string `json:"finished_at,omitempty"`
	// RestartCount is how many times the command has been restarted.
	RestartCount int `json:"restart_count"`
	// Error contains error details when the command is in failed state.
	Error string `json:"error,omitempty"`
}

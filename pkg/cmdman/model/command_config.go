package model

import (
	"errors"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

// CommandConfig is the canonical command configuration stored in CommandConfig.JSON.
type CommandConfig struct {
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
	// Tty controls whether the command is attached to a pseudo-terminal.
	Tty bool `json:"tty"`
	// ScrollbackBytes is the scrollback buffer size in bytes.
	ScrollbackBytes int `json:"scrollback_bytes"`
	// LogDriver controls how command output is persisted to disk.
	LogDriver logdriver.LogDriver `json:"log_driver"`
	// LogOpts is a driver-specific bag of options, mirroring podman's
	// `--log-opt KEY=VALUE` mechanism. Valid keys depend on LogDriver.
	LogOpts map[string]string `json:"log_opts,omitempty"`
	// Labels are user-defined key-value metadata.
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations are system metadata (e.g., auto-remove).
	Annotations map[string]string `json:"annotations,omitempty"`
	// CommandDir is the per-command directory path.
	CommandDir string `json:"command_dir"`
}

// CommandState stores mutable runtime fields in CommandState.JSON.
type CommandState struct {
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

// Validate rejects incomplete command configs so runtime code can assume values are present.
func (c *CommandConfig) Validate() error {
	if err := c.ValidateCreate(); err != nil {
		return err
	}
	if c.CommandDir == "" {
		return errors.New("command config: command_dir is empty")
	}
	return nil
}

// ValidateCreate validates caller-supplied config before derived fields are filled in.
func (c *CommandConfig) ValidateCreate() error {
	if c == nil {
		return errors.New("command config is nil")
	}
	if len(c.Argv) == 0 {
		return errors.New("command config: argv is empty")
	}
	if c.Dir == "" {
		return errors.New("command config: dir is empty")
	}
	if len(c.Env) == 0 {
		return errors.New("command config: env is empty")
	}
	if !IsRestartPolicy(string(c.RestartPolicy)) {
		return fmt.Errorf("command config: invalid restart policy %q", c.RestartPolicy)
	}
	if c.StopSignal != "" {
		if _, _, err := hrstr.ParseSignal(c.StopSignal); err != nil {
			return fmt.Errorf("command config: invalid stop_signal %q: %w", c.StopSignal, err)
		}
	}
	if c.ScrollbackBytes <= 0 {
		return fmt.Errorf(
			"command config: scrollback_bytes must be positive: %d",
			c.ScrollbackBytes,
		)
	}
	if !IsLogDriver(string(c.LogDriver)) {
		return fmt.Errorf("command config: invalid log_driver %q", c.LogDriver)
	}
	for k, v := range c.LogOpts {
		if err := logdriver.ValidateOpt(string(c.LogDriver), k, v); err != nil {
			return fmt.Errorf("command config: %w", err)
		}
	}
	return nil
}

// BackfillCommandConfigDefaults populates fields that may be missing from
// command configs persisted before they were introduced. It only fills
// fields that are unambiguously equivalent to the old behavior.
func BackfillCommandConfigDefaults(cfg *CommandConfig) {
	if cfg.LogDriver == "" {
		cfg.LogDriver = DefaultLogDriver
	}
}

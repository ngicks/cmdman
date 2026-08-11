package model

import (
	"errors"
	"fmt"

	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

// CommandConfig is the canonical command configuration stored in CommandConfig.JSON.
type CommandConfig struct {
	Argv          []string      `json:"argv"`
	Dir           string        `json:"dir,omitzero"`
	Env           []string      `json:"env,omitzero"`
	RestartPolicy RestartPolicy `json:"restart_policy"`
	// MaxRetries caps the number of automatic restarts under the "on-failure"
	// policy. Zero means unlimited. It is only valid with "on-failure".
	MaxRetries      int                 `json:"max_retries,omitzero"`
	StopSignal      string              `json:"stop_signal,omitzero"`
	Tty             bool                `json:"tty"`
	ScrollbackBytes int                 `json:"scrollback_bytes"`
	LogDriver       logdriver.LogDriver `json:"log_driver"`
	// LogOpts is a driver-specific bag of options, mirroring podman's
	// `--log-opt KEY=VALUE` mechanism. Valid keys depend on LogDriver.
	LogOpts map[string]string `json:"log_opts,omitzero"`
	Labels  map[string]string `json:"labels,omitzero"`
	// Annotations hold system metadata rather than user-defined labels.
	Annotations map[string]string `json:"annotations,omitzero"`
	// Hooks configures what happens to the sequences the monitor captures from
	// this command's output (D17/D40). It overrides the global defaults map;
	// an event this set does not name falls back to it.
	Hooks      HookSet `json:"hooks,omitzero"`
	CommandDir string  `json:"command_dir"`
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
	if c.MaxRetries < 0 {
		return fmt.Errorf("command config: max_retries must be non-negative: %d", c.MaxRetries)
	}
	if c.MaxRetries > 0 && c.RestartPolicy != RestartPolicyOnFailure {
		return fmt.Errorf(
			"command config: max_retries is only valid with restart policy %q",
			RestartPolicyOnFailure,
		)
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
	if err := c.Hooks.Validate(); err != nil {
		return fmt.Errorf("command config: %w", err)
	}
	return nil
}

// BackfillDefaults populates fields that may be missing from persisted
// configs before they were introduced. It only fills fields that are
// unambiguously equivalent to the old behavior.
func (c *CommandConfig) BackfillDefaults() {
	if c.LogDriver == "" {
		c.LogDriver = DefaultLogDriver
	}
}

// CommandState stores mutable runtime fields in CommandState.JSON.
type CommandState struct {
	// MonitorPID identifies the process supervising this command.
	MonitorPID int `json:"monitor_pid,omitzero"`
	// SocketPath is the monitor's Unix-domain gRPC endpoint.
	SocketPath string `json:"socket_path,omitzero"`
	// StartedAt and FinishedAt are RFC3339 command lifecycle timestamps.
	StartedAt    string `json:"started_at,omitzero"`
	FinishedAt   string `json:"finished_at,omitzero"`
	RestartCount int    `json:"restart_count"`
	Error        string `json:"error,omitzero"`
}

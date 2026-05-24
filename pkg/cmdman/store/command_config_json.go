package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver/k8sfile"
)

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
	// Tty controls whether the command is attached to a pseudo-terminal.
	Tty bool `json:"tty"`
	// ScrollbackBytes is the scrollback buffer size in bytes.
	ScrollbackBytes int `json:"scrollback_bytes"`
	// LogDriver controls how command output is persisted to disk.
	LogDriver LogDriver `json:"log_driver"`
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

// ConfigPath returns the full path to this command's config file.
func (c *CommandConfigJSON) ConfigPath() string {
	return filepath.Join(c.CommandDir, ConfigFileName)
}

// LogPath returns the full path to this command's log file when a file-
// based log driver is configured. LogOpts["path"] takes precedence over
// the per-command default. Empty when neither is set.
func (c *CommandConfigJSON) LogPath() string {
	if p, ok := c.LogOpts[LogOptPath]; ok && p != "" {
		return p
	}
	if c.CommandDir == "" {
		return ""
	}
	return filepath.Join(c.CommandDir, k8sfile.DefaultLogFileName)
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
		if _, _, err := ParseSignal(c.StopSignal); err != nil {
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
		if err := ValidateLogOpt(c.LogDriver, k, v); err != nil {
			return fmt.Errorf("command config: %w", err)
		}
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
	backfillCommandConfigDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// backfillCommandConfigDefaults populates fields that may be missing from
// command configs persisted before they were introduced. It only fills
// fields that are unambiguously equivalent to the old behavior.
func backfillCommandConfigDefaults(cfg *CommandConfigJSON) {
	if cfg.LogDriver == "" {
		cfg.LogDriver = DefaultLogDriver
	}
}

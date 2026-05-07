package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/dustin/go-humanize"
)

// ConfigFileName is the fixed name of the per-command configuration file.
const ConfigFileName = "config.json"

// LogFileName is the fixed name of the per-command log file when a file-
// based log driver is in use.
const LogFileName = "console.log"

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

// LogDriver determines how the monitor persists command output.
type LogDriver string

const (
	// LogDriverK8sFile writes a per-command log file in the same format
	// podman uses for its k8s-file driver (a.k.a. json-file). Each entry
	// is `<RFC3339Nano> <stream> <F|P> <content>\n`.
	LogDriverK8sFile LogDriver = "k8s-file"
	// LogDriverNone disables on-disk log capture.
	LogDriverNone LogDriver = "none"
)

// DefaultLogDriver is the log driver used when no explicit value is supplied.
const DefaultLogDriver = LogDriverK8sFile

// IsLogDriver reports whether s is a valid LogDriver value.
func IsLogDriver(s string) bool {
	switch LogDriver(s) {
	case LogDriverK8sFile, LogDriverNone:
		return true
	}
	return false
}

// LogOpt key constants. These mirror keys in podman's `--log-opt`
// vocabulary; only a subset is currently implemented.
const (
	// LogOptPath overrides the on-disk log file path for file-based
	// drivers. The value must be an absolute path.
	LogOptPath = "path"
	// LogOptMaxSize caps the size of the active log file. When a write
	// would push the file to or past this byte count, the k8s-file driver
	// rotates: the active file becomes <path>.1, the previous .1 becomes
	// .2, and so on. Accepted as a human-readable string parsed by
	// dustin/go-humanize.ParseBytes (e.g. "10mb", "1KiB", "1024"). The
	// empty string and "0" both disable the cap.
	LogOptMaxSize = "max-size"
	// LogOptMaxFile caps the total number of log files kept (active +
	// archives). When a rotation would push the count past this value,
	// the oldest archive is dropped. Only meaningful when LogOptMaxSize
	// is set; with LogOptMaxFile <= 1 the active file is truncated in
	// place instead of being rotated. Empty / "0" means "unset".
	LogOptMaxFile = "max-file"
)

// IsValidLogOpt reports whether key is meaningful for the given driver.
func IsValidLogOpt(driver LogDriver, key string) bool {
	switch driver {
	case LogDriverK8sFile:
		switch key {
		case LogOptPath, LogOptMaxSize, LogOptMaxFile:
			return true
		}
	}
	return false
}

// ValidateLogOpt checks that key is meaningful for the driver and that
// value satisfies any per-key constraints.
func ValidateLogOpt(driver LogDriver, key, value string) error {
	if !IsValidLogOpt(driver, key) {
		return fmt.Errorf("log_opt %q not valid for driver %q", key, driver)
	}
	switch key {
	case LogOptPath:
		if !filepath.IsAbs(value) {
			return fmt.Errorf("log_opt %q must be an absolute path: %q", key, value)
		}
	case LogOptMaxSize:
		if _, err := ParseLogMaxSize(value); err != nil {
			return fmt.Errorf("log_opt %q: %w", key, err)
		}
	case LogOptMaxFile:
		if _, err := ParseLogMaxFile(value); err != nil {
			return fmt.Errorf("log_opt %q: %w", key, err)
		}
	}
	return nil
}

// ParseLogMaxSize parses a max-size log-opt value into a non-negative
// byte count. It accepts the inputs supported by
// dustin/go-humanize.ParseBytes — bare integers ("1024"), SI suffixes
// ("10mb", "10 MB"), and IEC suffixes ("10mib"). The empty string and
// "0" both mean "no limit" and return 0 with no error.
func ParseLogMaxSize(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := humanize.ParseBytes(value)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", value, err)
	}
	if n > math.MaxInt64 {
		return 0, fmt.Errorf("size %q overflows int64", value)
	}
	return int64(n), nil
}

// ParseLogMaxFile parses a max-file log-opt value into a positive file
// count. The empty string and "0" both mean "unset" and return 0;
// non-empty values must be a positive integer.
func ParseLogMaxFile(value string) (int, error) {
	if value == "" || value == "0" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid count %q: %w", value, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("count %q must be >= 1", value)
	}
	return n, nil
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
	return filepath.Join(c.CommandDir, LogFileName)
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

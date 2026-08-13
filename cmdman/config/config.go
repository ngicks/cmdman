// Package config holds cmdman's process-wide configuration (Config), its
// path-helper methods, the CMDMAN_* environment variable names, and the
// config-file resolution logic. It is a leaf package so both cmdman and
// cmdman/monitor can depend on it without a cycle.
//
// Configuration is layered, lowest precedence first:
//
//	defaults < config file < environment < flags
//
// [DefaultConfig] is the lowest layer; [Load] overlays the config file and the
// environment onto it through the single merge primitive [PartialConfig.Apply];
// the ./cmd wiring layer applies explicitly-set flags on top.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"github.com/caarlos0/env/v11"

	"github.com/ngicks/cmdman/cmdman/eventlog"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

const (
	// ENV_CMDMAN_DATA_DIR and ENV_CMDMAN_RUNTIME_DIR name the two env-settable
	// config fields. [Load] does not read them directly - the names live in
	// PartialConfig's env tags and are parsed by caarlos0/env - but callers that
	// build a child process environment need them (see WithCommandContextEnv).
	ENV_CMDMAN_DATA_DIR    = "CMDMAN_DATA_DIR"
	ENV_CMDMAN_RUNTIME_DIR = "CMDMAN_RUNTIME_DIR"
	// ENV_CMDMAN_CMD_DATA_DIR and ENV_CMDMAN_CMD_ID describe a supervised
	// command's own context; they are exported to the command, never read back
	// into a Config.
	ENV_CMDMAN_CMD_DATA_DIR = "CMDMAN_CMD_DATA_DIR"
	ENV_CMDMAN_CMD_ID       = "CMDMAN_CMD_ID"
	// ENV_CMDMAN_CONF overrides the config-file path. When unset, the file at
	// <user config dir>/cmdman/config.json is consulted if it exists. It is the
	// one variable read by hand: the path is needed before parsing, and it is
	// not a Config field.
	ENV_CMDMAN_CONF = "CMDMAN_CONF"
)

// Config is the materialized process-wide configuration, after every layer
// (defaults < config file < environment < flags) has been applied. Its fields
// are value types - the merged config is always concrete - and carry both json
// and yaml tags so it can be marshaled for display and the file format can
// change without touching the field set. The config file is NOT decoded into
// Config: [PartialConfig] is the decode target.
//
//nolint:lll // json/yaml tags; one field per line, never wrap tags
type Config struct {
	DataDir                string              `json:"data_dir" yaml:"data_dir"`
	RuntimeDir             string              `json:"runtime_dir" yaml:"runtime_dir"`
	DefaultWorkingDir      string              `json:"default_working_dir" yaml:"default_working_dir"`
	DefaultEnvironment     []string            `json:"-" yaml:"-"`
	DefaultScrollbackBytes int                 `json:"default_scrollback_bytes" yaml:"default_scrollback_bytes"`
	DefaultLogDriver       logdriver.LogDriver `json:"default_log_driver" yaml:"default_log_driver"`
	// EventWatcherKind selects the backend used by event-log subscribers
	// (the `events` subcommand and any caller of cmdman.Service.Events). Valid
	// values are "inotify" (linux only) and "poll".
	EventWatcherKind eventlog.WatcherKind `json:"event_watcher_kind" yaml:"event_watcher_kind"`
	// DefaultHooks configures OSC/BEL hooks for every command that does not
	// name the event itself in model.CommandConfig.Hooks (D17/D40). It is a
	// config-file field only: there is no flag or environment variable for it.
	DefaultHooks model.HookSet `json:"default_hooks" yaml:"default_hooks"`
	// DefaultFrame names the frame def applied when no def is named
	// explicitly (D15): a bare name resolved under the frame config dir
	// ([FrameConfigDir], <name>.yaml/.yml) or a path, exactly as a frame
	// flag would pass it. Empty means no default frame (D16). It is a
	// config-file field only: there is no flag or environment variable for
	// it; the frame verbs supply their own flag on top.
	DefaultFrame string `json:"default_frame" yaml:"default_frame"`
	// ConfigPath is the --config value [Load] resolved this Config with; it is
	// "" when the file came from $CMDMAN_CONF or the default location. It is
	// provenance rather than a setting - no layer can set it, so it has no
	// [PartialConfig] mirror and no serialized key - and it exists so a process
	// that re-execs the binary (the monitor, the TUI popup) can forward
	// --config: a child inherits the environment but not the flags.
	ConfigPath string `json:"-" yaml:"-"`
}

// DefaultConfig is the lowest-precedence layer: the built-in defaults, with no
// config file and no environment variable consulted.
//
// It deviates from the canonical infallible DefaultConfig() because the default
// working directory comes from os.Getwd. The default data dir is best-effort
// instead: it is left empty when no home directory can be resolved, so a run
// that gets its dirs from the config file, $CMDMAN_DATA_DIR or --data-dir needs
// no $HOME, and [Config.Validate] reports the empty value if none of them does.
func DefaultConfig() (Config, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("get working directory: %w", err)
	}
	return Config{
		DataDir:                defaultDataDir(),
		RuntimeDir:             defaultRuntimeDir(),
		DefaultWorkingDir:      workingDir,
		DefaultEnvironment:     os.Environ(),
		DefaultScrollbackBytes: store.DefaultScrollbackBytes,
		DefaultLogDriver:       model.DefaultLogDriver,
		EventWatcherKind:       defaultEventWatcherKind(),
	}, nil
}

// Validate ensures all required fields are explicitly populated.
func (c Config) Validate() error {
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
	case !model.IsLogDriver(string(c.DefaultLogDriver)):
		return fmt.Errorf(
			"cmdman config: invalid default log driver %q",
			c.DefaultLogDriver,
		)
	case !eventlog.IsWatcherKind(string(c.EventWatcherKind)):
		return fmt.Errorf(
			"cmdman config: invalid event watcher kind %q",
			c.EventWatcherKind,
		)
	}
	if err := c.DefaultHooks.Validate(); err != nil {
		return fmt.Errorf("cmdman config: default %w", err)
	}
	return nil
}

// EventLogPath returns the configured event log file path. It lives next
// to the SQLite database.
func (c Config) EventLogPath() (string, error) {
	if c.DataDir == "" {
		return "", errors.New("cmdman config: data dir is empty")
	}
	return filepath.Join(c.DataDir, "events.log"), nil
}

// DBPath returns the configured SQLite database path.
func (c Config) DBPath() (string, error) {
	if c.DataDir == "" {
		return "", errors.New("cmdman config: data dir is empty")
	}
	return filepath.Join(c.DataDir, "commands.db"), nil
}

// CommandDir returns the configured per-command directory.
func (c Config) CommandDir(id string) (string, error) {
	if c.DataDir == "" {
		return "", errors.New("cmdman config: data dir is empty")
	}
	if id == "" {
		return "", errors.New("cmdman config: command id is empty")
	}
	return filepath.Join(c.DataDir, "commands", id), nil
}

// MonitorRuntimeDir returns the configured per-command runtime directory.
func (c Config) MonitorRuntimeDir(id string) (string, error) {
	if c.RuntimeDir == "" {
		return "", errors.New("cmdman config: runtime dir is empty")
	}
	if id == "" {
		return "", errors.New("cmdman config: command id is empty")
	}
	return filepath.Join(c.RuntimeDir, "cmd", id), nil
}

// MonitorSocketPath returns the configured Unix socket path for a monitor.
func (c Config) MonitorSocketPath(id string) (string, error) {
	runtimeDir, err := c.MonitorRuntimeDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, "monitor.sock"), nil
}

// MonitorPIDPath returns the configured PID file path for a monitor.
func (c Config) MonitorPIDPath(id string) (string, error) {
	runtimeDir, err := c.MonitorRuntimeDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, "pid"), nil
}

// PartialConfig is the exported sparse mirror of [Config]'s serialized shape
// (the same keys): a nil field means "absent, leave the lower layer", a set one
// is an explicit value. It is the decode target for the config file AND the
// struct [Load] fills from the environment via caarlos0/env, so both layers
// merge through one method, [PartialConfig.Apply].
//
// Only DataDir and RuntimeDir carry an env tag; every other field is
// file-only. The CMDMAN_ prefix is applied once via envOptions, so the tags
// hold the bare names (DATA_DIR -> CMDMAN_DATA_DIR). A present-but-empty
// variable is treated as absent by caarlos0/env, matching the pre-existing
// "empty means unset" behavior of these two variables.
//
// DefaultEnvironment is not file- or env-settable (json/yaml "-"): it is the
// environment handed to supervised commands, which callers override
// programmatically.
//
//nolint:lll // triple json/yaml/env tags; one field per line, never wrap tags
type PartialConfig struct {
	DataDir                *string               `json:"data_dir,omitzero" yaml:"data_dir,omitempty" env:"DATA_DIR"`
	RuntimeDir             *string               `json:"runtime_dir,omitzero" yaml:"runtime_dir,omitempty" env:"RUNTIME_DIR"`
	DefaultWorkingDir      *string               `json:"default_working_dir,omitzero" yaml:"default_working_dir,omitempty"`
	DefaultEnvironment     []string              `json:"-" yaml:"-"`
	DefaultScrollbackBytes *int                  `json:"default_scrollback_bytes,omitzero" yaml:"default_scrollback_bytes,omitempty"`
	DefaultLogDriver       *logdriver.LogDriver  `json:"default_log_driver,omitzero" yaml:"default_log_driver,omitempty"`
	EventWatcherKind       *eventlog.WatcherKind `json:"event_watcher_kind,omitzero" yaml:"event_watcher_kind,omitempty"`
	DefaultHooks           model.HookSet         `json:"default_hooks,omitzero" yaml:"default_hooks,omitempty"`
	DefaultFrame           *string               `json:"default_frame,omitzero" yaml:"default_frame,omitempty"`
}

// Apply overlays p's present fields onto base and returns the merged Config.
// Merge rules by field kind:
//   - scalar: a non-nil pointer overwrites, explicit zero included.
//   - map (DefaultHooks): deep-merged key by key into a fresh map (incoming
//     wins, base-only keys kept, base not mutated).
//   - slice (DefaultEnvironment): a non-nil incoming slice overwrites
//     wholesale; nil leaves the base.
func (p PartialConfig) Apply(base Config) Config {
	if p.DataDir != nil {
		base.DataDir = *p.DataDir
	}
	if p.RuntimeDir != nil {
		base.RuntimeDir = *p.RuntimeDir
	}
	if p.DefaultWorkingDir != nil {
		base.DefaultWorkingDir = *p.DefaultWorkingDir
	}
	if p.DefaultEnvironment != nil {
		base.DefaultEnvironment = p.DefaultEnvironment
	}
	if p.DefaultScrollbackBytes != nil {
		base.DefaultScrollbackBytes = *p.DefaultScrollbackBytes
	}
	if p.DefaultLogDriver != nil {
		base.DefaultLogDriver = *p.DefaultLogDriver
	}
	if p.EventWatcherKind != nil {
		base.EventWatcherKind = *p.EventWatcherKind
	}
	if p.DefaultHooks != nil {
		merged := make(model.HookSet, len(base.DefaultHooks)+len(p.DefaultHooks))
		maps.Copy(merged, base.DefaultHooks)
		maps.Copy(merged, p.DefaultHooks)
		base.DefaultHooks = merged
	}
	if p.DefaultFrame != nil {
		base.DefaultFrame = *p.DefaultFrame
	}
	return base
}

// envOptions configures caarlos0/env for the env layer in [Load]. The variable
// names live in PartialConfig's env tags; the CMDMAN_ prefix is applied here,
// yielding CMDMAN_DATA_DIR and CMDMAN_RUNTIME_DIR.
var envOptions = env.Options{Prefix: "CMDMAN_"}

// Load assembles defaults < config file < environment through
// [PartialConfig.Apply] and validates the result. The ./cmd layer applies
// explicitly-set flags on top (flags win). flagPath is the --config value ("",
// when the flag is unset) and is kept on the result as [Config.ConfigPath].
//
// The env layer fills a PartialConfig with caarlos0/env: a field is set
// (non-nil) only when its variable is present and non-empty, so absent ones
// leave the lower layer untouched. The error is non-nil only when a present
// value fails to parse - a hard error that aborts startup.
func Load(flagPath string) (Config, error) {
	cfg, err := DefaultConfig()
	if err != nil {
		return Config{}, err
	}

	cfg.ConfigPath = flagPath

	path, err := configPath(flagPath)
	if err != nil {
		return Config{}, err
	}
	filePartial, err := unmarshalConfigFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg = filePartial.Apply(cfg)

	var envPartial PartialConfig
	if err := env.ParseWithOptions(&envPartial, envOptions); err != nil {
		return Config{}, err
	}
	cfg = envPartial.Apply(cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// unmarshalConfigFile only reads + decodes; it never merges. It decodes into a
// fresh zero PartialConfig and returns the zero value when the file does not
// exist (or when path is "", i.e. no config location could be resolved). A
// non-ENOENT read error or a JSON parse error aborts.
func unmarshalConfigFile(path string) (PartialConfig, error) {
	if path == "" {
		return PartialConfig{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return PartialConfig{}, nil
		}
		return PartialConfig{}, fmt.Errorf("read config %q: %w", path, err)
	}
	var p PartialConfig
	if err := json.Unmarshal(b, &p); err != nil {
		return PartialConfig{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return p, nil
}

// ComposeConfigDir returns the directory that holds named compose projects: the
// "compose" subdirectory of the config directory (the directory containing the
// config file).
//
// Examples:
//
//	$CMDMAN_CONF=/etc/cmdman/config.json -> /etc/cmdman/compose
//	default                              -> <user config dir>/cmdman/compose
//
// Unlike [Load] it does not see the --config flag: its callers sit deep inside
// compose project discovery, so only $CMDMAN_CONF and the default location are
// consulted.
//
// Returns "" (without an error) when no config path can be determined (e.g. no
// $HOME) - there is simply nowhere to look.
func ComposeConfigDir() (string, error) {
	return configSubDir("compose")
}

// FrameConfigDir returns the directory that holds named frame definitions: the
// "frame" subdirectory of the config directory, resolved exactly like
// [ComposeConfigDir] - the --config flag is not seen here either.
//
// Examples:
//
//	$CMDMAN_CONF=/etc/cmdman/config.json -> /etc/cmdman/frame
//	default                              -> <user config dir>/cmdman/frame
//
// Returns "" (without an error) when no config path can be determined.
func FrameConfigDir() (string, error) {
	return configSubDir("frame")
}

func configSubDir(name string) (string, error) {
	path, err := configPath("")
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}
	return filepath.Join(filepath.Dir(path), name), nil
}

// configPath resolves the config file path: the --config flag value, else
// $CMDMAN_CONF, else <user config dir>/cmdman/config.json.
//
// os.UserConfigDir honors $XDG_CONFIG_HOME and is platform-native. Its error -
// no resolvable config dir, e.g. no $HOME - is swallowed into an empty path
// rather than propagated: a run configured entirely by flags and environment
// has nowhere to look for a file, which is not a failure.
func configPath(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if path := os.Getenv(ENV_CMDMAN_CONF); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", nil
	}
	return filepath.Join(dir, "cmdman", "config.json"), nil
}

// defaultDataDir is $XDG_DATA_HOME/cmdman, else ~/.local/share/cmdman, else ""
// when no home directory is resolvable - an environment that supplies the data
// dir some other way is not an error here (see [DefaultConfig]).
func defaultDataDir() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "cmdman")
}

func defaultRuntimeDir() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, "cmdman")
}

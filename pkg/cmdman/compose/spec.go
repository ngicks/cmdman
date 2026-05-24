// Package compose provides config parsing, normalization, hashing, dependency
// graph validation, and reconciliation planning for cmdman compose.
package compose

import (
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// LabelPrefix is the reserved label key prefix. User labels using this prefix are rejected.
const LabelPrefix = "cmdman.compose."

// Reserved label keys.
const (
	LabelProject    = "cmdman.compose.project"
	LabelCommand    = "cmdman.compose.command"
	LabelConfigHash = "cmdman.compose.config-hash"
	LabelVersion    = "cmdman.compose.version"
	LabelWorkdir    = "cmdman.compose.workdir"
	LabelFile       = "cmdman.compose.file"

	LabelVersionValue = "1"
)

// AfterCondition is the dependency condition for a command's after spec.
type AfterCondition string

const (
	ConditionCompleted             AfterCondition = "completed"
	ConditionStarted               AfterCondition = "started"
	ConditionCompletedSuccessfully AfterCondition = "completed_successfully"
)

// ---- Raw YAML structs -------------------------------------------------------

// RawComposeSpec is the top-level raw YAML model.
type RawComposeSpec struct {
	Name     string                `yaml:"name"`
	WorkDir  string                `yaml:"work_dir"`
	Commands map[string]RawCommand `yaml:"commands"`
	// Unknown captures unrecognized top-level keys so Normalize can warn about them.
	Unknown map[string]any `yaml:",inline"`
}

// RawCommand is the raw YAML shape for a single command.
type RawCommand struct {
	Dir             string               `yaml:"dir"`
	Args            []string             `yaml:"args"`
	Env             []string             `yaml:"env"`
	EnvFile         []EnvFileSpec        `yaml:"env_file"`
	Labels          map[string]string    `yaml:"labels"`
	RestartPolicy   string               `yaml:"restart_policy"`
	StopSignal      string               `yaml:"stop_signal"`
	Tty             bool                 `yaml:"tty"`
	ScrollbackBytes int                  `yaml:"scrollback_bytes"`
	LogDriver       string               `yaml:"log_driver"`
	LogOpts         map[string]string    `yaml:"log_opts"`
	After           map[string]AfterSpec `yaml:"after"`
	// Unknown captures unrecognized per-command keys so Normalize can warn about them.
	Unknown map[string]any `yaml:",inline"`
}

// EnvFileSpec describes an env file to load for a command.
type EnvFileSpec struct {
	Path     string `yaml:"path"`
	Required *bool  `yaml:"required"` // pointer so we can detect absence; defaults to true
}

// AfterSpec is the dependency specification for a command.
// Name is filled from the map key during normalization.
type AfterSpec struct {
	Name      string         // filled from map key during normalization
	Condition AfterCondition `yaml:"condition"` // defaults to "completed"
}

// ---- Normalized model -------------------------------------------------------

// ComposeSpec is the validated, resolved compose configuration.
type ComposeSpec struct {
	// ComposeFile is the absolute path to the compose file used.
	ComposeFile string
	// Project is the effective project name.
	Project string
	// WorkDir is the canonical absolute work directory.
	WorkDir string
	// Commands is the ordered list of normalized commands.
	Commands []Command
}

// Command is a single command after normalization.
type Command struct {
	// Name is the compose command name (YAML map key).
	Name string
	// Dir is the resolved absolute working directory for this command.
	Dir string
	// Args is the interpolated argv.
	Args []string
	// Env is the merged environment (env_file + env: overrides), interpolated,
	// in KEY=VALUE form. Does NOT include OS environment; callers inject that.
	Env []string
	// Labels are user-supplied labels. Reserved cmdman.compose.* labels are absent here;
	// they are added by Plan when building CreateRequest inputs.
	Labels map[string]string
	// RestartPolicy from the YAML.
	RestartPolicy model.RestartPolicy
	// StopSignal from the YAML.
	StopSignal string
	// Tty from the YAML.
	Tty bool
	// ScrollbackBytes from the YAML.
	ScrollbackBytes int
	// LogDriver from the YAML.
	LogDriver logdriver.LogDriver
	// LogOpts from the YAML.
	LogOpts map[string]string
	// After is the expanded dependency list.
	After []AfterSpec
	// GeneratedName is the deterministic cmdman command name:
	// <workdir-hash>-<escaped-project>-<escaped-command>
	GeneratedName string
}

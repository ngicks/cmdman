// Package compose provides config parsing, normalization, hashing, dependency
// graph validation, and reconciliation planning for cmdman compose.
package compose

import (
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
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
	LabelAfter      = "cmdman.compose.after"
	// LabelScaleIndex is the 1-based replica index of a scaled command's
	// instance (e.g. "2" for the api-2 replica). Every compose-created command
	// carries it; an unscaled command's sole instance has index "1".
	LabelScaleIndex = "cmdman.compose.scale-index"
	// LabelScale is the desired replica count of the command this instance
	// belongs to, recorded so stored state can be read back without the file.
	LabelScale = "cmdman.compose.scale"

	LabelVersionValue = "1"
)

// AfterCondition is the dependency condition for a command's after spec.
type AfterCondition string

const (
	ConditionCompleted             AfterCondition = "completed"
	ConditionRunning               AfterCondition = "running"
	ConditionCompletedSuccessfully AfterCondition = "completed_successfully"
)

func (c AfterCondition) Validate() error {
	switch c {
	case ConditionCompleted, ConditionRunning, ConditionCompletedSuccessfully:
		return nil
	default:
		return fmt.Errorf(
			"unknown condition %q (allowed: completed, running, completed_successfully)",
			c,
		)
	}
}

// ---- Raw YAML structs -------------------------------------------------------

// RawComposeSpec is the top-level raw YAML model.
type RawComposeSpec struct {
	Name     string                `yaml:"name" json:"name"`
	WorkDir  string                `yaml:"work_dir" json:"work_dir"`
	Commands map[string]RawCommand `yaml:"commands" json:"commands"`
	// Mux is the embedded cmdman mux layout, decoded straight into the
	// cmdman-layer spec type (nil when the file has no "mux:" section). Its
	// leaves still carry project-scoped service names; `cmdman compose mux`
	// resolves those to commands at run time. Storing a typed *mux.Spec
	// (rather than a raw yaml node or bytes) keeps any decoder-specific type
	// off this struct, so the spec format is not pinned to YAML.
	Mux *mux.Spec `yaml:"mux,omitempty" json:"mux,omitzero"`
	// Unknown captures unrecognized top-level keys so Normalize can warn about them.
	Unknown map[string]any `yaml:",inline" json:"-"`
}

// RawCommand is the raw YAML shape for a single command.
type RawCommand struct {
	Dir             string               `yaml:"dir" json:"dir"`
	Args            []string             `yaml:"args" json:"args"`
	Env             []string             `yaml:"env" json:"env"`
	EnvFile         []EnvFileSpec        `yaml:"env_file" json:"env_file"`
	Labels          map[string]string    `yaml:"labels" json:"labels"`
	RestartPolicy   string               `yaml:"restart_policy" json:"restart_policy"`
	StopSignal      string               `yaml:"stop_signal" json:"stop_signal"`
	Tty             bool                 `yaml:"tty" json:"tty"`
	ScrollbackBytes int                  `yaml:"scrollback_bytes" json:"scrollback_bytes"`
	LogDriver       string               `yaml:"log_driver" json:"log_driver"`
	LogOpts         map[string]string    `yaml:"log_opts" json:"log_opts"`
	After           map[string]AfterSpec `yaml:"after" json:"after"`
	// Scale is the desired replica count. A pointer so absence (nil → default 1)
	// is distinguishable from an explicit value; Normalize rejects values < 1.
	Scale *int `yaml:"scale" json:"scale"`
	// Unknown captures unrecognized per-command keys so Normalize can warn about them.
	Unknown map[string]any `yaml:",inline" json:"-"`
}

// EnvFileSpec describes an env file to load for a command.
type EnvFileSpec struct {
	Path     string `yaml:"path" json:"path"`
	Required *bool  `yaml:"required" json:"required"` //nolint:lll // pointer so we can detect absence; defaults to true
}

// AfterSpec is the dependency specification for a command.
// Name is filled from the map key during normalization.
type AfterSpec struct {
	Name string `yaml:"name" json:"name"`
	// Condition defaults to "completed".
	Condition AfterCondition `yaml:"condition" json:"condition"`
}

func (a AfterSpec) Validate() error {
	if a.Name == "" {
		return fmt.Errorf("dependency name is empty")
	}
	if err := a.Condition.Validate(); err != nil {
		return fmt.Errorf("dependency %q: %w", a.Name, err)
	}
	return nil
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
	// Mux is the embedded "mux:" layout from the compose file (nil when
	// absent), with project-scoped service names still in its leaves.
	// `cmdman compose mux` resolves those to commands and runs it through the
	// same path as standalone `cmdman mux`.
	Mux *mux.Spec
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
	// MaxRetries is the on-failure restart cap parsed from restart_policy
	// ("on-failure:N"). Zero means unlimited.
	MaxRetries int
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
	// Scale is the desired replica count (>= 1). Each replica is a distinct
	// cmdman command named <GeneratedName>-<index> for index in 1..Scale.
	Scale int
	// GeneratedName is the deterministic cmdman command base name:
	// <workdir-hash>-<escaped-project>-<escaped-command>. The concrete per-replica
	// command name appends the 1-based scale index (see InstanceName).
	GeneratedName string
}

// InstanceNames returns the per-replica cmdman command names for this command,
// ordered by scale index (index 1 first). It always returns at least one name.
func (c Command) InstanceNames() []string {
	n := max(c.Scale, 1)
	out := make([]string, n)
	for i := range out {
		out[i] = InstanceName(c.GeneratedName, i+1)
	}
	return out
}

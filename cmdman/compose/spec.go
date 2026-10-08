// Package compose provides config parsing, normalization, hashing, dependency
// graph validation, and reconciliation planning for cmdman compose.
package compose

import (
	"fmt"
	"iter"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/mux"
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
	// LabelHooks is the command's normalized hooks as a JSON array of
	// [LifecycleHook], recorded so each replica can run its hooks without the
	// file. It is absent when the command declares no hooks.
	LabelHooks = "cmdman.compose.hooks"

	LabelVersionValue = "1"
)

// Labels of a compose job: the cmdman command that runs a compose verb for one
// project in the background, so the verb outlives whatever asked for it.
//
// A job never carries [LabelProject] or [LabelWorkdir], by which every compose
// verb selects the commands of a project, so no verb mistakes it for a replica,
// least of all the down it runs. It names its project with [LabelJobProject]
// and [LabelJobWorkdir] instead.
const (
	// LabelJob is the compose verb the job runs, such as [JobDown].
	LabelJob = "cmdman.compose.job"
	// LabelJobWorkdir is the canonical work directory of the project the job
	// acts on.
	LabelJobWorkdir = "cmdman.compose.job.workdir"
	// LabelJobProject is the name of that project.
	LabelJobProject = "cmdman.compose.job.project"
)

// JobDown is the [LabelJob] value of a job running `cmdman compose down`.
const JobDown = "down"

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
	Dir     string        `yaml:"dir" json:"dir"`
	Args    []string      `yaml:"args" json:"args"`
	Env     []string      `yaml:"env" json:"env"`
	EnvFile []EnvFileSpec `yaml:"env_file" json:"env_file"`
	// ImportHostEnv controls whether the host environment is imported as the
	// base for this command (env_file + env: are layered on top as overrides).
	// A pointer so absence (nil → default true) is distinguishable from an
	// explicit false.
	ImportHostEnv *bool `yaml:"import_host_env" json:"import_host_env"` //nolint:lll // pointer detects absence; defaults to true
	// InjectEnv controls whether the command's environment gets CMDMAN_DATA_DIR,
	// CMDMAN_RUNTIME_DIR, CMDMAN_CMD_DATA_DIR and CMDMAN_CMD_ID pointing at this
	// command. A pointer so absence (nil → default true) is distinguishable from
	// an explicit false.
	InjectEnv       *bool                `yaml:"inject_env" json:"inject_env"`
	Labels          map[string]string    `yaml:"labels" json:"labels"`
	RestartPolicy   string               `yaml:"restart_policy" json:"restart_policy"`
	StopSignal      string               `yaml:"stop_signal" json:"stop_signal"`
	StopGracePeriod string               `yaml:"stop_grace_period" json:"stop_grace_period"`
	Stop            *RawStopCommand      `yaml:"stop" json:"stop"`
	Tty             bool                 `yaml:"tty" json:"tty"`
	ScrollbackBytes int                  `yaml:"scrollback_bytes" json:"scrollback_bytes"`
	LogDriver       string               `yaml:"log_driver" json:"log_driver"`
	LogOpts         map[string]string    `yaml:"log_opts" json:"log_opts"`
	After           map[string]AfterSpec `yaml:"after" json:"after"`
	// Scale is the desired replica count. A pointer so absence (nil → default 1)
	// is distinguishable from an explicit value; Normalize rejects values < 1.
	Scale *int               `yaml:"scale" json:"scale"`
	Hooks []RawLifecycleHook `yaml:"hooks" json:"hooks"`
	// Unknown captures unrecognized per-command keys so Normalize can warn about them.
	Unknown map[string]any `yaml:",inline" json:"-"`
}

// RawLifecycleHook is the raw YAML shape for one item of a command's hooks:
// list. A nil event field means the item does not set that event.
type RawLifecycleHook struct {
	Name       string            `yaml:"name" json:"name"`
	Resource   string            `yaml:"resource" json:"resource"`
	CreatePre  *RawLifecycleExec `yaml:"create_pre" json:"create_pre"`
	CreatePost *RawLifecycleExec `yaml:"create_post" json:"create_post"`
	StartPre   *RawLifecycleExec `yaml:"start_pre" json:"start_pre"`
	StartPost  *RawLifecycleExec `yaml:"start_post" json:"start_post"`
	StopPre    *RawLifecycleExec `yaml:"stop_pre" json:"stop_pre"`
	StopPost   *RawLifecycleExec `yaml:"stop_post" json:"stop_post"`
	RemovePre  *RawLifecycleExec `yaml:"remove_pre" json:"remove_pre"`
	RemovePost *RawLifecycleExec `yaml:"remove_post" json:"remove_post"`
	// Unknown captures unrecognized item keys so Normalize can warn about them.
	Unknown map[string]any `yaml:",inline" json:"-"`
}

// events yields the event fields h sets, in [LifecycleEvent] declaration order.
func (h RawLifecycleHook) events() iter.Seq2[LifecycleEvent, *RawLifecycleExec] {
	return func(yield func(LifecycleEvent, *RawLifecycleExec) bool) {
		for _, f := range [...]struct {
			event LifecycleEvent
			exec  *RawLifecycleExec
		}{
			{LifecycleCreatePre, h.CreatePre},
			{LifecycleCreatePost, h.CreatePost},
			{LifecycleStartPre, h.StartPre},
			{LifecycleStartPost, h.StartPost},
			{LifecycleStopPre, h.StopPre},
			{LifecycleStopPost, h.StopPost},
			{LifecycleRemovePre, h.RemovePre},
			{LifecycleRemovePost, h.RemovePost},
		} {
			if f.exec != nil && !yield(f.event, f.exec) {
				return
			}
		}
	}
}

// RawLifecycleExec is the raw YAML shape for one hook event. In YAML it is
// either an argv list or a mapping with args and on_error; see
// [RawLifecycleExec.UnmarshalYAML].
type RawLifecycleExec struct {
	Args    []string `yaml:"args" json:"args"`
	OnError OnError  `yaml:"on_error" json:"on_error"`
	// Unknown captures unrecognized keys of the mapping form so Normalize can
	// warn about them.
	Unknown map[string]any `yaml:",inline" json:"-"`
}

// UnmarshalYAML accepts either a sequence, read as the argv list with the
// default on_error, or a mapping with the [RawLifecycleExec] fields.
func (e *RawLifecycleExec) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var args []string
		if err := node.Decode(&args); err != nil {
			return err
		}
		*e = RawLifecycleExec{Args: args}
		return nil
	case yaml.MappingNode:
		type rawExec RawLifecycleExec
		var r rawExec
		if err := node.Decode(&r); err != nil {
			return err
		}
		*e = RawLifecycleExec(r)
		return nil
	default:
		return fmt.Errorf(
			"line %d: hook event must be an argv list or a mapping with args and on_error",
			node.Line,
		)
	}
}

// RawStopCommand is the raw YAML shape of a command's stop: field. In YAML it
// is either an argv list or a mapping with args; see
// [RawStopCommand.UnmarshalYAML].
type RawStopCommand struct {
	Args []string `yaml:"args" json:"args"`
	// Unknown captures unrecognized keys of the mapping form so Normalize can
	// warn about them.
	Unknown map[string]any `yaml:",inline" json:"-"`
}

// UnmarshalYAML accepts either a sequence, read as the argv list, or a mapping
// with the [RawStopCommand] fields.
func (s *RawStopCommand) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var args []string
		if err := node.Decode(&args); err != nil {
			return err
		}
		*s = RawStopCommand{Args: args}
		return nil
	case yaml.MappingNode:
		type rawStop RawStopCommand
		var r rawStop
		if err := node.Decode(&r); err != nil {
			return err
		}
		*s = RawStopCommand(r)
		return nil
	default:
		return fmt.Errorf("line %d: stop must be an argv list or a mapping with args", node.Line)
	}
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
	// ComposeFile and WorkDir are absolute; WorkDir is canonicalized.
	ComposeFile string
	Project     string
	WorkDir     string
	// WorkDirDeclared reports that WorkDir was chosen — by the file's work_dir:
	// or by the caller's override — rather than defaulted to the working
	// directory the load happened to run in. A project that declares none runs
	// wherever it is started from, which is a different thing from one that
	// belongs to a directory.
	WorkDirDeclared bool
	// Commands preserves normalization order.
	Commands []Command
	// Mux is the embedded "mux:" layout from the compose file (nil when
	// absent), with project-scoped service names still in its leaves.
	// `cmdman compose mux` resolves those to commands and runs it through the
	// same path as standalone `cmdman mux`.
	Mux *mux.Spec
}

// Command is a single command after normalization.
type Command struct {
	Name string
	Dir  string
	Args []string
	// Env is the merged environment (env_file + env: overrides), interpolated,
	// in KEY=VALUE form. Does NOT include OS environment; callers inject that.
	Env []string
	// ImportHostEnv controls whether the host environment is imported as the
	// base for this command, with Env layered on top as overrides. Resolved from
	// RawCommand.ImportHostEnv during normalization; defaults to true.
	ImportHostEnv bool
	// InjectEnv controls whether the command's environment gets CMDMAN_DATA_DIR,
	// CMDMAN_RUNTIME_DIR, CMDMAN_CMD_DATA_DIR and CMDMAN_CMD_ID pointing at this
	// command. Resolved from RawCommand.InjectEnv during normalization; defaults
	// to true.
	InjectEnv bool
	// Labels are user-supplied labels. Reserved cmdman.compose.* labels are absent here;
	// they are added by Plan when building CreateRequest inputs.
	Labels        map[string]string
	RestartPolicy model.RestartPolicy
	// MaxRetries is the on-failure restart cap parsed from restart_policy
	// ("on-failure:N"). Zero means unlimited.
	MaxRetries int
	StopSignal string
	// StopGracePeriod is the parsed stop_grace_period. Zero means unset.
	StopGracePeriod time.Duration
	// Stop is the stop: argv with its args interpolated. Nil means unset.
	Stop            []string
	Tty             bool
	ScrollbackBytes int
	LogDriver       logdriver.LogDriver
	LogOpts         map[string]string
	After           []AfterSpec
	// Hooks are the command's lifecycle hooks in declaration order, with event
	// args interpolated and every on_error resolved.
	Hooks []LifecycleHook
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

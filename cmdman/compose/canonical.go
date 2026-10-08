package compose

import (
	"fmt"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/mux"
)

// CanonicalSpec is a normalized [ComposeSpec] projected back onto the compose
// file schema with every value fully resolved: interpolation applied, paths
// made absolute, env_file + env: merged and sorted, restart_policy recomposed,
// and dependency conditions defaulted. It is a valid compose document — feeding
// it back to cmdman yields the same plan.
//
// The shape mirrors [RawComposeSpec]/[RawCommand] so the rendered output reads
// like the source file. Canonical form is YAML (the compose file's own format),
// so only YAML tags are defined. Ordering is deterministic: struct fields encode
// in declaration order, the YAML encoder sorts map keys, and Env is already
// sorted by normalization — so the output is stable across runs and across host
// environments.
type CanonicalSpec struct {
	Name     string                      `yaml:"name" json:"name"`
	WorkDir  string                      `yaml:"work_dir" json:"work_dir"`
	Commands map[string]CanonicalCommand `yaml:"commands" json:"commands"`
	Mux      *mux.Spec                   `yaml:"mux,omitempty" json:"mux,omitzero"`
}

// CanonicalCommand is one resolved command in a [CanonicalSpec]. Fields left at
// their zero value are omitted: normalization records only values the author set
// explicitly (cmdman applies runtime defaults later), so an omitted field means
// "not set in the compose file", not "set to the default".
type CanonicalCommand struct {
	Dir  string   `yaml:"dir" json:"dir"`
	Args []string `yaml:"args" json:"args"`
	Env  []string `yaml:"env,omitempty" json:"env,omitzero"`
	// ImportHostEnv is rendered only when it deviates from the default (true): a
	// pointer set to false emits "import_host_env: false", while the default
	// (nil) is omitted so the canonical document round-trips back to true.
	ImportHostEnv *bool `yaml:"import_host_env,omitempty" json:"import_host_env,omitzero"` //nolint:lll // dual yaml+json snake_case tags exceed the line limit
	// InjectEnv is rendered only when it deviates from the default (true): a
	// pointer set to false emits "inject_env: false", while the default (nil) is
	// omitted so the canonical document round-trips back to true.
	InjectEnv       *bool                     `yaml:"inject_env,omitempty" json:"inject_env,omitzero"` //nolint:lll // dual yaml+json snake_case tags exceed the line limit
	Labels          map[string]string         `yaml:"labels,omitempty" json:"labels,omitzero"`
	RestartPolicy   string                    `yaml:"restart_policy,omitempty" json:"restart_policy,omitzero"`       //nolint:lll // dual yaml+json snake_case tags exceed the line limit
	StopSignal      string                    `yaml:"stop_signal,omitempty" json:"stop_signal,omitzero"`             //nolint:lll // dual yaml+json snake_case tags exceed the line limit
	StopGracePeriod string                    `yaml:"stop_grace_period,omitempty" json:"stop_grace_period,omitzero"` //nolint:lll // dual yaml+json snake_case tags exceed the line limit
	Stop            []string                  `yaml:"stop,omitempty" json:"stop,omitzero"`
	Tty             bool                      `yaml:"tty,omitempty" json:"tty,omitzero"`
	ScrollbackBytes int                       `yaml:"scrollback_bytes,omitempty" json:"scrollback_bytes,omitzero"` //nolint:lll // dual yaml+json snake_case tags exceed the line limit
	LogDriver       string                    `yaml:"log_driver,omitempty" json:"log_driver,omitzero"`
	LogOpts         map[string]string         `yaml:"log_opts,omitempty" json:"log_opts,omitzero"`
	After           map[string]CanonicalAfter `yaml:"after,omitempty" json:"after,omitzero"`
	Scale           int                       `yaml:"scale,omitempty" json:"scale,omitzero"`
	Hooks           []CanonicalLifecycleHook  `yaml:"hooks,omitempty" json:"hooks,omitzero"`
}

// CanonicalLifecycleHook is one resolved hook item in a [CanonicalCommand].
// Events the item does not set are omitted.
type CanonicalLifecycleHook struct {
	Name       string                  `yaml:"name" json:"name"`
	Resource   string                  `yaml:"resource,omitempty" json:"resource,omitzero"`
	CreatePre  *CanonicalLifecycleExec `yaml:"create_pre,omitempty" json:"create_pre,omitzero"`
	CreatePost *CanonicalLifecycleExec `yaml:"create_post,omitempty" json:"create_post,omitzero"`
	StartPre   *CanonicalLifecycleExec `yaml:"start_pre,omitempty" json:"start_pre,omitzero"`
	StartPost  *CanonicalLifecycleExec `yaml:"start_post,omitempty" json:"start_post,omitzero"`
	StopPre    *CanonicalLifecycleExec `yaml:"stop_pre,omitempty" json:"stop_pre,omitzero"`
	StopPost   *CanonicalLifecycleExec `yaml:"stop_post,omitempty" json:"stop_post,omitzero"`
	RemovePre  *CanonicalLifecycleExec `yaml:"remove_pre,omitempty" json:"remove_pre,omitzero"`
	RemovePost *CanonicalLifecycleExec `yaml:"remove_post,omitempty" json:"remove_post,omitzero"`
}

// CanonicalLifecycleExec is one resolved hook event, always in the mapping
// form. OnError is always populated ("fail" when the compose file omits it).
type CanonicalLifecycleExec struct {
	Args    []string `yaml:"args" json:"args"`
	OnError string   `yaml:"on_error" json:"on_error"`
}

// CanonicalAfter is the resolved dependency condition for one predecessor.
// Condition is always populated (normalization defaults it to "completed").
type CanonicalAfter struct {
	Condition string `yaml:"condition" json:"condition"`
}

// Canonicalize projects a normalized [ComposeSpec] onto its [CanonicalSpec]
// form. The input is assumed already validated by [Normalize].
func Canonicalize(spec ComposeSpec) CanonicalSpec {
	commands := make(map[string]CanonicalCommand, len(spec.Commands))
	for _, c := range spec.Commands {
		commands[c.Name] = canonicalCommand(c)
	}
	return CanonicalSpec{
		Name:     spec.Project,
		WorkDir:  spec.WorkDir,
		Commands: commands,
		Mux:      spec.Mux,
	}
}

func canonicalCommand(c Command) CanonicalCommand {
	var after map[string]CanonicalAfter
	if len(c.After) > 0 {
		after = make(map[string]CanonicalAfter, len(c.After))
		for _, a := range c.After {
			after[a.Name] = CanonicalAfter{Condition: string(a.Condition)}
		}
	}
	var importHostEnv *bool
	if !c.ImportHostEnv {
		// Default is true, so only an explicit false is rendered; true is omitted
		// and round-trips back to the default.
		disabled := false
		importHostEnv = &disabled
	}
	var injectEnv *bool
	if !c.InjectEnv {
		// Default is true, so only an explicit false is rendered; true is omitted
		// and round-trips back to the default.
		disabled := false
		injectEnv = &disabled
	}
	return CanonicalCommand{
		Dir:             c.Dir,
		Args:            c.Args,
		Env:             c.Env,
		ImportHostEnv:   importHostEnv,
		InjectEnv:       injectEnv,
		Labels:          c.Labels,
		RestartPolicy:   canonicalRestartPolicy(c.RestartPolicy, c.MaxRetries),
		StopSignal:      c.StopSignal,
		StopGracePeriod: canonicalStopGracePeriod(c.StopGracePeriod),
		Stop:            c.Stop,
		Tty:             c.Tty,
		ScrollbackBytes: c.ScrollbackBytes,
		LogDriver:       string(c.LogDriver),
		LogOpts:         c.LogOpts,
		After:           after,
		Scale:           canonicalScale(c.Scale),
		Hooks:           canonicalLifecycleHooks(c.Hooks),
	}
}

func canonicalLifecycleHooks(hooks []LifecycleHook) []CanonicalLifecycleHook {
	if len(hooks) == 0 {
		return nil
	}
	out := make([]CanonicalLifecycleHook, len(hooks))
	for i, h := range hooks {
		exec := func(ev LifecycleEvent) *CanonicalLifecycleExec {
			e, ok := h.Events[ev]
			if !ok {
				return nil
			}
			return &CanonicalLifecycleExec{Args: e.Args, OnError: string(e.OnError.resolved())}
		}
		out[i] = CanonicalLifecycleHook{
			Name:       h.Name,
			Resource:   h.Resource,
			CreatePre:  exec(LifecycleCreatePre),
			CreatePost: exec(LifecycleCreatePost),
			StartPre:   exec(LifecycleStartPre),
			StartPost:  exec(LifecycleStartPost),
			StopPre:    exec(LifecycleStopPre),
			StopPost:   exec(LifecycleStopPost),
			RemovePre:  exec(LifecycleRemovePre),
			RemovePost: exec(LifecycleRemovePost),
		}
	}
	return out
}

// canonicalScale renders the scale only when it deviates from the default of 1,
// so an unscaled command omits the field (consistent with the other
// omit-when-default canonical fields).
func canonicalScale(scale int) int {
	if scale <= 1 {
		return 0
	}
	return scale
}

// canonicalStopGracePeriod renders the grace period as a Go duration string, and
// an unset (zero) period as empty so the field is omitted.
func canonicalStopGracePeriod(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

// canonicalRestartPolicy recomposes the restart_policy string that normalization
// split into a policy and a retry cap. The "on-failure" policy with a positive
// cap renders as "on-failure:N"; every other policy renders bare, and an empty
// policy stays empty (omitted on output).
func canonicalRestartPolicy(policy model.RestartPolicy, maxRetries int) string {
	if policy == "" {
		return ""
	}
	if policy == model.RestartPolicyOnFailure && maxRetries > 0 {
		return fmt.Sprintf("%s:%d", policy, maxRetries)
	}
	return string(policy)
}

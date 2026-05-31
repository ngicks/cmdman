package compose

import (
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
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
	Name     string                      `yaml:"name"`
	WorkDir  string                      `yaml:"work_dir"`
	Commands map[string]CanonicalCommand `yaml:"commands"`
	Mux      *mux.Spec                   `yaml:"mux,omitempty"`
}

// CanonicalCommand is one resolved command in a [CanonicalSpec]. Fields left at
// their zero value are omitted: normalization records only values the author set
// explicitly (cmdman applies runtime defaults later), so an omitted field means
// "not set in the compose file", not "set to the default".
type CanonicalCommand struct {
	Dir             string                    `yaml:"dir"`
	Args            []string                  `yaml:"args"`
	Env             []string                  `yaml:"env,omitempty"`
	Labels          map[string]string         `yaml:"labels,omitempty"`
	RestartPolicy   string                    `yaml:"restart_policy,omitempty"`
	StopSignal      string                    `yaml:"stop_signal,omitempty"`
	Tty             bool                      `yaml:"tty,omitempty"`
	ScrollbackBytes int                       `yaml:"scrollback_bytes,omitempty"`
	LogDriver       string                    `yaml:"log_driver,omitempty"`
	LogOpts         map[string]string         `yaml:"log_opts,omitempty"`
	After           map[string]CanonicalAfter `yaml:"after,omitempty"`
}

// CanonicalAfter is the resolved dependency condition for one predecessor.
// Condition is always populated (normalization defaults it to "completed").
type CanonicalAfter struct {
	Condition string `yaml:"condition"`
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
	return CanonicalCommand{
		Dir:             c.Dir,
		Args:            c.Args,
		Env:             c.Env,
		Labels:          c.Labels,
		RestartPolicy:   canonicalRestartPolicy(c.RestartPolicy, c.MaxRetries),
		StopSignal:      c.StopSignal,
		Tty:             c.Tty,
		ScrollbackBytes: c.ScrollbackBytes,
		LogDriver:       string(c.LogDriver),
		LogOpts:         c.LogOpts,
		After:           after,
	}
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

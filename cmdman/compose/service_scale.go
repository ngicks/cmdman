package compose

import (
	"context"
	"fmt"
)

// ScaleOption configures [Service.Scale]. File/ProjectName/WorkDir are the
// [NormalizeOpts] trio the compose file is loaded with (`compose -f/-p` and the
// work directory override).
type ScaleOption struct {
	// File is an explicit compose file path or project name. When empty,
	// discovery is used.
	File string
	// ProjectName overrides the YAML top-level name:.
	ProjectName string
	// WorkDir overrides the YAML work_dir: and CWD fallback.
	WorkDir string
	// Scales maps a compose command (service) name to its desired replica
	// count, which must be >= 1. Names not declared by the compose file are an
	// error; commands left out keep the file's scale:.
	Scales map[string]int
}

// Scale sets the desired replica count of the named commands and reconciles to
// it: missing replicas are created and started, surplus ones stopped and
// removed. The override is ephemeral — it is applied to the loaded spec, never
// written back, so a later Up reverts to the file.
//
// The reconcile is an [Service.Up] scoped to the named commands, so the same
// Reporter, aggregation and partial-failure semantics apply; the result is
// returned rather than reduced to an error because a scale that reaches only
// some of its replicas is exactly what UpResult reports.
func (s *Service) Scale(ctx context.Context, opts ScaleOption) (*UpResult, error) {
	spec, err := LoadAndNormalize(NormalizeOpts{
		File:        opts.File,
		ProjectName: opts.ProjectName,
		WorkDir:     opts.WorkDir,
	})
	if err != nil {
		return nil, err
	}
	if err := applyScaleOverrides(&spec, opts.Scales); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(opts.Scales))
	for name := range opts.Scales {
		names = append(names, name)
	}
	return s.Up(ctx, spec, UpOption{
		CreateOption: CreateOption{CommandNames: names},
		StartOption:  StartOption{CommandNames: names},
	})
}

// applyScaleOverrides sets the requested replica counts on the matching spec
// commands, erroring when a named command is not declared in the compose file
// or the count is below one. The count guard lives here rather than only in the
// CLI's argument parsing because a programmatic caller reaches this path
// directly, and Plan reads a zero scale as an unscaled single instance
// (plan.go:240) — which would silently mean 1 instead of failing.
func applyScaleOverrides(spec *ComposeSpec, scales map[string]int) error {
	index := make(map[string]int, len(spec.Commands))
	for i, c := range spec.Commands {
		index[c.Name] = i
	}
	for name, num := range scales {
		i, ok := index[name]
		if !ok {
			return fmt.Errorf("unknown compose command %q", name)
		}
		if num < 1 {
			return fmt.Errorf("invalid scale for %q: must be >= 1, got %d", name, num)
		}
		spec.Commands[i].Scale = num
	}
	return nil
}

package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// cmdmanEntry is a type alias to avoid importing store throughout the service files.
type cmdmanEntry = store.CommandEntry

// ProjectSelection describes how the target project was resolved: a loaded Spec
// (compose file), or WorkDir plus an optional Project. An empty Project selects
// every command in WorkDir (cwd-based query).
type ProjectSelection struct {
	// Spec is non-nil when a compose file was loaded. When set, DAG ordering is
	// available for stop/restart.
	Spec *ComposeSpec
	// WorkDir is the effective work directory (resolved: --workdir > YAML work_dir > CWD).
	WorkDir string
	// Project is the effective project name, or "" to match every command in WorkDir.
	Project string
}

// filterByCommandNames returns only the entries whose LabelCommand matches one
// of the provided names.
func filterByCommandNames(entries []cmdmanEntry, names []string) []cmdmanEntry {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	out := entries[:0:0]
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		cmdName := e.ConfigJSON.Labels[LabelCommand]
		if _, ok := set[cmdName]; ok {
			out = append(out, e)
		}
	}
	return out
}

// buildIDByCommand returns a map from compose command name (LabelCommand) to
// the cmdman entry ID for the supplied entries.
func buildIDByCommand(entries []cmdmanEntry) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		name := e.ConfigJSON.Labels[LabelCommand]
		if name != "" {
			m[name] = e.ID
		}
	}
	return m
}

// validateCommandNames rejects supplied command-name filters that don't match
// any compose command in the available set. The available set comes from the
// loaded spec when available, or from the LabelCommand values of the existing
// project-labeled entries otherwise. Returns nil when names is empty (no
// filter) or every name is recognized.
func validateCommandNames(
	names []string,
	spec *ComposeSpec,
	entries []cmdmanEntry,
) error {
	if len(names) == 0 {
		return nil
	}
	known := make(map[string]struct{})
	if spec != nil {
		for _, nc := range spec.Commands {
			known[nc.Name] = struct{}{}
		}
	} else {
		for _, e := range entries {
			if e.ConfigJSON == nil {
				continue
			}
			if n := e.ConfigJSON.Labels[LabelCommand]; n != "" {
				known[n] = struct{}{}
			}
		}
	}
	var unknown []string
	for _, n := range names {
		if _, ok := known[n]; !ok {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return fmt.Errorf("unknown compose command(s): %v", unknown)
}

// reverseLayers reverses a slice of layers in-place.
func reverseLayers(layers [][]string) {
	for i, j := 0, len(layers)-1; i < j; i, j = i+1, j-1 {
		layers[i], layers[j] = layers[j], layers[i]
	}
}

// LoadOrProject resolves the project selection for operations that do not
// require a compose file (stop, restart, down, ...).
//
// Resolution order:
//  1. If a compose file is discoverable (explicit File or default file names in
//     CWD), load and normalize it into a ProjectSelection with Spec set.
//  2. An explicit --file that fails to load is an error; there is no fallback.
//  3. Otherwise build a selection scoped to the working directory (--workdir or
//     the process CWD). --project-name narrows it; when absent the selection
//     matches every command in that workdir, so these operations work from the
//     project directory without -f or --project-name.
func LoadOrProject(opts NormalizeOpts) (ProjectSelection, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return ProjectSelection{}, fmt.Errorf("get working directory: %w", err)
	}

	filePath, raw, discoverErr := DiscoverFile(cwd, opts)
	if discoverErr == nil {
		spec, err := Normalize(context.Background(), filePath, raw, opts)
		if err != nil {
			return ProjectSelection{}, err
		}
		return ProjectSelection{
			Spec:    &spec,
			WorkDir: spec.WorkDir,
			Project: spec.Project,
		}, nil
	}
	if opts.File != "" {
		return ProjectSelection{}, discoverErr
	}

	// No compose file: query by working directory. --project-name narrows the
	// selection; when empty it matches every command in this workdir (cwd), which
	// is how down/stop/... work from the project directory without -f.
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = cwd
	}
	if !filepath.IsAbs(workDir) {
		workDir = filepath.Join(cwd, workDir)
	}
	workDir = filepath.Clean(workDir)

	return ProjectSelection{
		Spec:    nil,
		WorkDir: workDir,
		Project: opts.ProjectName,
	}, nil
}

// projectLabels builds the List label filter for a project selection. WorkDir
// always scopes the query; the project label is included only when known. An
// empty project therefore matches every command in the workdir — the cwd-based
// query used when no compose file or --project-name is given.
func projectLabels(workDir, project string) map[string]string {
	labels := map[string]string{LabelWorkdir: workDir}
	if project != "" {
		labels[LabelProject] = project
	}
	return labels
}

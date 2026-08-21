package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ngicks/cmdman/cmdman/store"
)

// cmdmanEntry is a type alias to avoid importing store throughout the service files.
type cmdmanEntry = store.CommandEntry

// ProjectSelection describes how the target project was resolved: a loaded Spec
// (compose file), or WorkDir plus an optional Project. An empty Project selects
// every command in WorkDir (cwd-based query).
type ProjectSelection struct {
	// Spec is non-nil when a compose file was loaded. When absent, operations
	// reconstruct dependency order from stored compose labels when possible.
	Spec *ComposeSpec
	// WorkDir is the effective work directory (resolved: --workdir > YAML work_dir > CWD).
	WorkDir string
	// Project is the effective project name, or "" to match every command in WorkDir.
	Project string
}

// ProjectIdentity returns the opaque mux ownership identity for this project:
// [GenerateProjectIdentity](workdirHash(WorkDir), Project). It is the value to
// pass as [mux.RunOptions.Identity] and [mux.DownOptions.Identity] for compose
// mux dashboards.
//
// An unnamed project still gets one, hashed from the work directory alone: the
// directory is the half of a project's identity that is always there, so two
// unnamed projects in two directories own two windows rather than fighting over
// the one window a shared name would have named.
func (s ProjectSelection) ProjectIdentity() string {
	return GenerateProjectIdentity(workdirHash(s.WorkDir), s.Project)
}

// MuxWindowName derives the cmdman-owned window name for a compose project:
// "cmdman-<project>", or plain "cmdman" when the project is unnamed. It is the
// default [mux.RunOptions.WindowName] for compose mux dashboards when the
// caller supplies no display-name override — the label the window wears, not
// what it is found by (that is [ProjectSelection.ProjectIdentity]), so two
// projects sharing a name may well share it.
func (s ProjectSelection) MuxWindowName() string {
	if s.Project != "" {
		return "cmdman-" + s.Project
	}
	return "cmdman"
}

// SelectionFromSpec builds the selection of an already-loaded compose spec: the
// project it declares, scoped to the work directory [Normalize] resolved. It is
// the no-reload path for callers that already hold a spec (e.g. the mux tail of
// `compose up --mux`).
func SelectionFromSpec(spec *ComposeSpec) ProjectSelection {
	return ProjectSelection{
		Spec:    spec,
		WorkDir: spec.WorkDir,
		Project: spec.Project,
	}
}

// ResolveMuxSelection resolves the compose project for the `compose mux`
// subcommand (the CLI entry point). An explicit opts.File loads exactly that
// project; without it the project is auto-selected from the composes associated
// with the current directory that declare a "mux:" section (see
// [SelectMuxProject]). Either way the resolved project must declare a "mux:"
// section.
func ResolveMuxSelection(opts NormalizeOpts) (ProjectSelection, error) {
	if opts.File == "" {
		return SelectMuxProject(opts)
	}
	selection, err := LoadOrProject(opts)
	if err != nil {
		return ProjectSelection{}, err
	}
	if selection.Spec == nil || selection.Spec.Mux == nil {
		return ProjectSelection{}, errors.New(
			`compose mux: missing "mux:" section in compose file`,
		)
	}
	return selection, nil
}

// ResolveMuxSelectionByName resolves the compose project for a mux operation
// driven by a project name (the TUI entry point). composeFile is used directly
// when set; otherwise it is resolved on demand from projectName. workDir is the
// directory the project stands in, "" leaving the file's work_dir: and the
// process CWD to answer: it is half of what identifies a project, so a caller
// that knows it and leaves it out resolves the named file against wherever it
// happens to be running. The resolved project must declare a "mux:" section.
//
// projectName means what `cmdman compose -p` means once composeFile names the
// file: it overrides the file's top-level name:, so a caller holding both is
// answered with the project it named rather than the one the file declares.
// Without a file the name is the file key instead and the declared name: stands
// — overriding it there would make `-f <name>` and `-p <name>` resolve the same
// file into two differently named projects, and only one of them owns the
// stored commands.
func ResolveMuxSelectionByName(
	projectName, composeFile, workDir string,
) (ProjectSelection, error) {
	opts := NormalizeOpts{File: composeFile, ProjectName: projectName, WorkDir: workDir}
	if composeFile == "" {
		opts.File, opts.ProjectName = projectName, ""
	}
	selection, err := LoadOrProject(opts)
	if err != nil {
		return ProjectSelection{}, err
	}
	if selection.Spec == nil {
		return ProjectSelection{}, fmt.Errorf(
			"mux: no compose file found for project %q", projectName,
		)
	}
	if selection.Spec.Mux == nil {
		return ProjectSelection{}, fmt.Errorf(
			"mux: project %q has no mux section", projectName,
		)
	}
	return selection, nil
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

// commandNameOf returns the compose command name (LabelCommand) recorded on an
// entry, or "" when the entry has no stored config or label.
func commandNameOf(e cmdmanEntry) string {
	if e.ConfigJSON == nil {
		return ""
	}
	return e.ConfigJSON.Labels[LabelCommand]
}

// buildIDsByCommand groups the cmdman entry IDs by compose command name
// (LabelCommand), so a command with multiple replicas maps to all of its IDs.
func buildIDsByCommand(entries []cmdmanEntry) map[string][]string {
	m := make(map[string][]string, len(entries))
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		name := e.ConfigJSON.Labels[LabelCommand]
		if name != "" {
			m[name] = append(m[name], e.ID)
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
		return specSelection(filePath, raw, opts)
	}
	if opts.File != "" {
		return ProjectSelection{}, discoverErr
	}

	// No compose file: query by working directory. --project-name narrows the
	// selection; when empty it matches every command in this workdir (cwd), which
	// is how down/stop/... work from the project directory without -f.
	return workdirSelection(cwd, opts), nil
}

// LoadOrWorkdir resolves the project selection for read-only listing operations
// (ps) that report stored reality for a working directory.
//
// Unlike [LoadOrProject] it does NOT auto-discover a default compose file: an
// auto-discovered cmd-compose.yaml must not silently narrow a status listing to
// a single project when several projects share the workdir (a co-located
// cmd-compose.yaml project plus named projects whose work_dir points here).
//
// Resolution order:
//  1. An explicit --file is loaded and normalized; the selection is scoped to
//     its (workdir, project). A --file that fails to load is an error.
//  2. Otherwise the selection is scoped to the working directory (--workdir or
//     the process CWD). --project-name narrows it; when absent the selection
//     matches every command in that workdir, listing every co-located project.
func LoadOrWorkdir(opts NormalizeOpts) (ProjectSelection, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return ProjectSelection{}, fmt.Errorf("get working directory: %w", err)
	}

	if opts.File != "" {
		filePath, raw, err := DiscoverFile(cwd, opts)
		if err != nil {
			return ProjectSelection{}, err
		}
		return specSelection(filePath, raw, opts)
	}

	return workdirSelection(cwd, opts), nil
}

// specSelection normalizes a decoded compose file into a selection scoped to the
// spec's (workdir, project).
func specSelection(
	filePath string,
	raw RawComposeSpec,
	opts NormalizeOpts,
) (ProjectSelection, error) {
	spec, err := Normalize(context.Background(), filePath, raw, opts)
	if err != nil {
		return ProjectSelection{}, err
	}
	return SelectionFromSpec(&spec), nil
}

// workdirSelection builds a fileless selection scoped to a working directory:
// WorkDir is --workdir (resolved against CWD) or CWD; Project is --project-name,
// where "" matches every command in the workdir.
func workdirSelection(cwd string, opts NormalizeOpts) ProjectSelection {
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
	}
}

// SelectMuxProject auto-selects the compose project for `compose mux` when the
// caller gives no explicit -f. The candidate set is every compose "associated
// with the current working directory" that declares a top-level mux: section:
//
//   - the cwd compose file (cmd-compose.yaml/.yml), when present; and
//   - every named project under the default compose dir (see ListMuxProjects)
//     whose resolved work_dir equals the cwd.
//
// It returns the sole candidate's selection, erroring when none or more than
// one associated compose declares a mux: section (the ambiguous case). opts
// carries the caller's --workdir / --project-name overrides for the cwd file;
// opts.File must be empty, since an explicit -f bypasses this auto-selection.
func SelectMuxProject(opts NormalizeOpts) (ProjectSelection, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return ProjectSelection{}, fmt.Errorf("get working directory: %w", err)
	}
	cwd = filepath.Clean(cwd)

	type muxCandidate struct {
		label string
		sel   ProjectSelection
	}
	var candidates []muxCandidate

	// The cwd compose file (cmd-compose.yaml/.yml), when it declares mux:.
	// LoadOrProject returns Spec==nil (no error) when no cwd file exists, and
	// surfaces a real error when a present file fails to load.
	cwdSel, err := LoadOrProject(opts)
	if err != nil {
		return ProjectSelection{}, err
	}
	if cwdSel.Spec != nil && cwdSel.Spec.Mux != nil {
		candidates = append(candidates, muxCandidate{
			label: filepath.Base(cwdSel.Spec.ComposeFile),
			sel:   cwdSel,
		})
	}

	// Named projects under the default compose dir whose work_dir is the cwd.
	projects, err := ListMuxProjects()
	if err != nil {
		return ProjectSelection{}, err
	}
	for _, p := range projects {
		if filepath.Clean(p.Spec.WorkDir) != cwd {
			continue
		}
		spec := p.Spec
		candidates = append(candidates, muxCandidate{
			label: p.Name,
			sel:   SelectionFromSpec(&spec),
		})
	}

	switch len(candidates) {
	case 0:
		return ProjectSelection{}, errors.New(
			`compose mux: no compose with a "mux:" section is associated with this ` +
				"directory; pass -f <file|project>")
	case 1:
		return candidates[0].sel, nil
	default:
		labels := make([]string, len(candidates))
		for i, c := range candidates {
			labels[i] = c.label
		}
		return ProjectSelection{}, fmt.Errorf(
			`compose mux: multiple composes associated with this directory have a `+
				`"mux:" section (%s); select one with -f`,
			strings.Join(labels, ", "))
	}
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

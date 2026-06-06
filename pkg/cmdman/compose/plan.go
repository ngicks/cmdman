package compose

import (
	"fmt"
	"maps"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// ActionKind classifies the action required for a single command in a compose plan.
type ActionKind string

const (
	// ActionCreate means the command does not exist and must be created.
	ActionCreate ActionKind = "create"
	// ActionRecreate means the command exists but its config hash has changed;
	// it must be deleted and re-created. A running command is stopped first by the
	// caller before the remove + create.
	ActionRecreate ActionKind = "recreate"
	// ActionUnchanged means the command exists and its config hash matches desired.
	ActionUnchanged ActionKind = "unchanged"
)

// CommandAction describes the required action for a single compose command.
type CommandAction struct {
	// Kind is the action type.
	Kind ActionKind
	// Desired is the normalized command from the compose spec.
	Desired Command
	// Existing is the current store entry, nil for ActionCreate.
	Existing *store.CommandEntry
	// DesiredHash is the computed hash of the desired command config.
	DesiredHash string
}

// Plan is the result of reconciling a desired compose spec against existing
// project-labeled commands.
type Plan struct {
	// Actions lists create/recreate/unchanged actions in topological order
	// (commands with no dependencies come first).
	Actions []CommandAction
	// Orphans lists existing commands that belong to the project but are absent
	// from the desired spec.
	Orphans []store.CommandEntry
}

// ComputePlan computes the reconciliation plan for the given desired spec against the
// provided existing store entries.
//
// existing should be the full result of Service.List with
//
//	Labels: {LabelWorkdir: spec.WorkDir, LabelProject: spec.Project}
//	AllStates: true
//
// Errors:
//   - within-WorkDir project collision: an existing command has the same
//     (workdir, project) labels but a different cmdman.compose.file label than
//     spec.ComposeFile. Both file paths are included in the error.
func ComputePlan(spec ComposeSpec, existing []store.CommandEntry) (Plan, error) {
	// Validate the dependency graph before building actions.
	if err := ValidateDAG(spec.Commands); err != nil {
		return Plan{}, fmt.Errorf("dependency graph: %w", err)
	}

	// Index desired commands by name.
	desiredByName := make(map[string]Command, len(spec.Commands))
	for _, c := range spec.Commands {
		desiredByName[c.Name] = c
	}

	// Index existing commands by compose command name label.
	existingByCommand := make(map[string]store.CommandEntry)
	var orphans []store.CommandEntry

	for _, e := range existing {
		cfg := e.ConfigJSON
		if cfg == nil {
			continue
		}
		cmdName := cfg.Labels[LabelCommand]
		existingFile := cfg.Labels[LabelFile]

		// Within-WorkDir collision: same (workdir, project) but different file.
		if existingFile != "" && existingFile != spec.ComposeFile {
			return Plan{}, fmt.Errorf(
				"compose project collision: project %q in workdir %q is already owned by %q; "+
					"conflicting file: %q; use --project-name to create a separate project",
				spec.Project, spec.WorkDir, existingFile, spec.ComposeFile,
			)
		}

		if _, wanted := desiredByName[cmdName]; wanted {
			existingByCommand[cmdName] = e
		} else {
			orphans = append(orphans, e)
		}
	}

	// Build topological layers for action ordering.
	layers, err := TopoLayers(spec.Commands)
	if err != nil {
		return Plan{}, fmt.Errorf("dependency graph: %w", err)
	}

	// Build name→Command index for layer lookup.
	cmdByName := make(map[string]Command, len(spec.Commands))
	for _, c := range spec.Commands {
		cmdByName[c.Name] = c
	}

	var actions []CommandAction

	for _, layer := range layers {
		for _, name := range layer {
			desired := cmdByName[name]

			desiredHash, err := Hash(desired)
			if err != nil {
				return Plan{}, fmt.Errorf("hash command %q: %w", name, err)
			}

			ex, exists := existingByCommand[name]
			if !exists {
				actions = append(actions, CommandAction{
					Kind:        ActionCreate,
					Desired:     desired,
					Existing:    nil,
					DesiredHash: desiredHash,
				})
				continue
			}

			storedHash := ""
			if ex.ConfigJSON != nil {
				storedHash = ex.ConfigJSON.Labels[LabelConfigHash]
			}

			if storedHash == desiredHash {
				actions = append(actions, CommandAction{
					Kind:        ActionUnchanged,
					Desired:     desired,
					Existing:    &ex,
					DesiredHash: desiredHash,
				})
			} else {
				actions = append(actions, CommandAction{
					Kind:        ActionRecreate,
					Desired:     desired,
					Existing:    &ex,
					DesiredHash: desiredHash,
				})
			}
		}
	}

	return Plan{
		Actions: actions,
		Orphans: orphans,
	}, nil
}

// BuildLabels returns the complete label map for a compose command, merging
// user labels with the reserved compose labels.
// configHash should be the output of Hash(cmd).
func BuildLabels(
	spec ComposeSpec,
	cmd Command,
	configHash string,
) map[string]string {
	labels := make(map[string]string, len(cmd.Labels)+6)
	maps.Copy(labels, cmd.Labels)
	labels[LabelProject] = spec.Project
	labels[LabelCommand] = cmd.Name
	labels[LabelConfigHash] = configHash
	labels[LabelVersion] = LabelVersionValue
	labels[LabelWorkdir] = spec.WorkDir
	labels[LabelFile] = spec.ComposeFile
	return labels
}

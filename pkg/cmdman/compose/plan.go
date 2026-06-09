package compose

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

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

// CommandAction describes the required action for a single compose command
// replica (one scale instance).
type CommandAction struct {
	// Kind is the action type.
	Kind ActionKind
	// Desired is the normalized command (service definition) from the compose
	// spec; every replica of the command shares it.
	Desired Command
	// ScaleIndex is the 1-based replica index this action targets.
	ScaleIndex int
	// InstanceName is the concrete cmdman command name for this replica
	// (Desired.GeneratedName with the scale-index suffix).
	InstanceName string
	// Existing is the current store entry, nil for ActionCreate.
	Existing *store.CommandEntry
	// DesiredHash is the computed hash of the desired command config.
	DesiredHash string
}

// Plan is the result of reconciling a desired compose spec against existing
// project-labeled commands.
type Plan struct {
	// Actions lists create/recreate/unchanged actions in topological order
	// (commands with no dependencies come first), one per desired replica.
	Actions []CommandAction
	// Orphans lists existing commands that belong to the project but whose
	// compose command name is absent from the desired spec.
	Orphans []store.CommandEntry
	// ExcessReplicas lists existing instances of a still-desired command whose
	// scale index exceeds the desired replica count — the surplus left by a
	// scale-down. Unlike orphans, these are always reconciled away (stopped and
	// removed) because reducing scale is an explicit instruction.
	ExcessReplicas []store.CommandEntry
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

	// Index existing commands by (compose command name, scale index). A desired
	// command whose existing instances exceed its replica count leaves the
	// surplus in existingByInstance after the desired loop consumes the in-range
	// ones; that surplus is the scale-down excess.
	existingByInstance := make(map[instanceKey]store.CommandEntry)
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
			existingByInstance[instanceKey{cmdName, scaleIndexOf(e)}] = e
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

			// All replicas share the same runtime config, hence the same hash.
			desiredHash, err := Hash(desired)
			if err != nil {
				return Plan{}, fmt.Errorf("hash command %q: %w", name, err)
			}

			scale := max(desired.Scale, 1)
			for idx := 1; idx <= scale; idx++ {
				key := instanceKey{name, idx}
				action := CommandAction{
					Desired:      desired,
					ScaleIndex:   idx,
					InstanceName: InstanceName(desired.GeneratedName, idx),
					DesiredHash:  desiredHash,
				}

				ex, exists := existingByInstance[key]
				if !exists {
					action.Kind = ActionCreate
					actions = append(actions, action)
					continue
				}
				// Consume the matched instance so it is not later flagged excess.
				delete(existingByInstance, key)

				storedHash := ""
				if ex.ConfigJSON != nil {
					storedHash = ex.ConfigJSON.Labels[LabelConfigHash]
				}
				action.Existing = &ex
				if storedHash == desiredHash {
					action.Kind = ActionUnchanged
				} else {
					action.Kind = ActionRecreate
				}
				actions = append(actions, action)
			}
		}
	}

	// Whatever desired-command instances remain unconsumed are surplus replicas
	// from a scale-down. Order them deterministically for stable reporting.
	var excess []store.CommandEntry
	for _, e := range existingByInstance {
		excess = append(excess, e)
	}
	slices.SortFunc(excess, func(a, b store.CommandEntry) int {
		return strings.Compare(a.Name, b.Name)
	})

	return Plan{
		Actions:        actions,
		Orphans:        orphans,
		ExcessReplicas: excess,
	}, nil
}

// instanceKey identifies one replica of a compose command by its command name
// and 1-based scale index.
type instanceKey struct {
	command    string
	scaleIndex int
}

// scaleIndexOf reads the 1-based scale index recorded on an existing entry. A
// missing or unparseable label yields 0, which never matches a desired index
// (>= 1), so such an entry is treated as surplus/orphan rather than a live
// replica.
func scaleIndexOf(e store.CommandEntry) int {
	if e.ConfigJSON == nil {
		return 0
	}
	n, err := strconv.Atoi(e.ConfigJSON.Labels[LabelScaleIndex])
	if err != nil {
		return 0
	}
	return n
}

// BuildLabels returns the complete label map for one replica of a compose
// command, merging user labels with the reserved compose labels. scaleIndex is
// the replica's 1-based index. configHash should be the output of Hash(cmd).
func BuildLabels(
	spec ComposeSpec,
	cmd Command,
	configHash string,
	scaleIndex int,
) map[string]string {
	labels := make(map[string]string, len(cmd.Labels)+9)
	maps.Copy(labels, cmd.Labels)
	labels[LabelProject] = spec.Project
	labels[LabelCommand] = cmd.Name
	labels[LabelConfigHash] = configHash
	labels[LabelVersion] = LabelVersionValue
	labels[LabelWorkdir] = spec.WorkDir
	labels[LabelFile] = spec.ComposeFile
	labels[LabelScaleIndex] = strconv.Itoa(scaleIndex)
	labels[LabelScale] = strconv.Itoa(max(cmd.Scale, 1))
	if len(cmd.After) > 0 {
		after, err := json.Marshal(cmd.After)
		if err != nil {
			panic(fmt.Sprintf("compose: marshal normalized after label: %v", err))
		}
		labels[LabelAfter] = string(after)
	}
	return labels
}

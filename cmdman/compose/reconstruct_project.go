package compose

import (
	"fmt"
	"slices"
)

func reconstructProjectFromMeta(
	selection ProjectSelection,
	entries []cmdmanEntry,
) (ComposeSpec, bool, error) {
	var (
		project string
		seen    bool
	)
	// Group replicas under their compose command name. Multiple entries sharing a
	// name are the scale replicas of one command, not a conflict.
	byName := make(map[string]*Command)
	var order []string
	for _, entry := range entries {
		if entry.ConfigJSON == nil {
			continue
		}
		labels := entry.ConfigJSON.Labels
		name := labels[LabelCommand]
		if name == "" {
			continue
		}
		entryProject := labels[LabelProject]
		if !seen {
			project = entryProject
			seen = true
		} else if entryProject != project {
			return ComposeSpec{}, false, nil
		}
		if existing, ok := byName[name]; ok {
			existing.Scale++
			continue
		}
		after, err := decodeAfterLabel(labels[LabelAfter])
		if err != nil {
			return ComposeSpec{}, false, fmt.Errorf("command %q: %w", name, err)
		}
		// Recover the base generated name by stripping this replica's scale-index
		// suffix, so Command.InstanceNames regenerates every replica's name.
		generatedName := stripScaleIndexSuffix(entry.Name, scaleIndexOf(entry))
		if generatedName == "" {
			generatedName = entry.ID
		}
		byName[name] = &Command{
			Name:          name,
			GeneratedName: generatedName,
			After:         after,
			Scale:         1,
		}
		order = append(order, name)
	}
	if len(order) == 0 {
		return ComposeSpec{}, false, nil
	}
	commands := make([]Command, 0, len(order))
	for _, name := range order {
		commands = append(commands, *byName[name])
	}
	slices.SortFunc(commands, func(a, b Command) int {
		return cmpCommandName(a.Name, b.Name)
	})
	spec := ComposeSpec{
		Project:  project,
		WorkDir:  selection.WorkDir,
		Commands: commands,
	}
	if err := ValidateDAG(spec.Commands); err != nil {
		return ComposeSpec{}, false, fmt.Errorf("stored dependency graph: %w", err)
	}
	return spec, true, nil
}

func cmpCommandName(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

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
	commands := make([]Command, 0, len(entries))
	names := make(map[string]struct{}, len(entries))
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
		if _, dup := names[name]; dup {
			return ComposeSpec{}, false, nil
		}
		names[name] = struct{}{}
		after, err := decodeAfterLabel(labels[LabelAfter])
		if err != nil {
			return ComposeSpec{}, false, fmt.Errorf("command %q: %w", name, err)
		}
		generatedName := entry.Name
		if generatedName == "" {
			generatedName = entry.ID
		}
		commands = append(commands, Command{
			Name:          name,
			GeneratedName: generatedName,
			After:         after,
		})
	}
	if len(commands) == 0 {
		return ComposeSpec{}, false, nil
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

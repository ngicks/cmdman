package mux

import "slices"

// CollectCycleTargets returns a sorted, deduplicated list of unpinned leaf
// command names (Scale == 0) from all layouts of spec. These are the commands
// that participate in cycle-scale.
func CollectCycleTargets(spec Spec) []string {
	seen := make(map[string]struct{})
	for _, layout := range spec.Layouts {
		collectUnpinnedLeafCommands(layout.Root, seen)
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// collectUnpinnedLeafCommands walks p recursively and adds the command name of
// each unpinned leaf (Scale == 0) to seen.
func collectUnpinnedLeafCommands(p PaneSpec, seen map[string]struct{}) {
	if p.IsLeaf() {
		if p.Scale == 0 {
			seen[p.Command] = struct{}{}
		}
		return
	}
	for _, child := range p.Panes {
		collectUnpinnedLeafCommands(child, seen)
	}
}

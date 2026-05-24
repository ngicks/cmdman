package compose

import (
	"fmt"
	"slices"
)

// ValidateDAG checks the dependency graph of the given commands for cycles.
// It also verifies that every dependency target names an existing command.
// Returns an error if any cycle or unknown dependency is found.
func ValidateDAG(commands []Command) error {
	nameSet := make(map[string]struct{}, len(commands))
	for _, c := range commands {
		nameSet[c.Name] = struct{}{}
	}

	for _, c := range commands {
		for _, dep := range c.After {
			if _, ok := nameSet[dep.Name]; !ok {
				return fmt.Errorf("command %q depends on unknown command %q", c.Name, dep.Name)
			}
		}
	}

	_, err := topoLayers(commands)
	return err
}

// TopoLayers returns a topological layering of the commands as a slice of
// slices. Commands in the same layer have no ordering dependency relative to
// each other and can run concurrently. Layer 0 contains all commands with no
// dependencies; each subsequent layer depends only on earlier layers.
//
// Returns an error if the graph contains a cycle or an unknown dependency.
func TopoLayers(commands []Command) ([][]string, error) {
	return topoLayers(commands)
}

// topoLayers is the internal implementation (Kahn's algorithm).
func topoLayers(commands []Command) ([][]string, error) {
	if len(commands) == 0 {
		return nil, nil
	}

	// Build adjacency: edges[name] = set of names that name depends on.
	// inDegree[name] = count of dependencies not yet satisfied.
	inDegree := make(map[string]int, len(commands))
	// dependents[dep] = list of commands that depend on dep
	dependents := make(map[string][]string, len(commands))

	nameSet := make(map[string]struct{}, len(commands))
	for _, c := range commands {
		nameSet[c.Name] = struct{}{}
		inDegree[c.Name] = 0 // ensure key exists with zero value
	}

	for _, c := range commands {
		for _, dep := range c.After {
			if _, ok := nameSet[dep.Name]; !ok {
				return nil, fmt.Errorf("command %q depends on unknown command %q", c.Name, dep.Name)
			}
			inDegree[c.Name]++
			dependents[dep.Name] = append(dependents[dep.Name], c.Name)
		}
	}

	var layers [][]string
	remaining := len(commands)

	for remaining > 0 {
		// Collect all commands with inDegree == 0.
		layer := make([]string, 0)
		for _, c := range commands {
			if inDegree[c.Name] == 0 {
				layer = append(layer, c.Name)
			}
		}
		if len(layer) == 0 {
			// All remaining commands have inDegree > 0 → cycle.
			cycle := detectCycle(commands, inDegree)
			return nil, fmt.Errorf("dependency cycle detected: %s", cycle)
		}

		// Sort for determinism.
		slices.Sort(layer)
		layers = append(layers, layer)

		// Remove processed nodes.
		for _, name := range layer {
			inDegree[name] = -1 // mark as processed
			remaining--
			for _, dep := range dependents[name] {
				inDegree[dep]--
			}
		}
	}

	return layers, nil
}

// detectCycle finds and formats a description of a cycle among commands whose
// inDegree > 0. It does a simple DFS to find the cycle members.
func detectCycle(commands []Command, inDegree map[string]int) string {
	// Collect names still in the graph.
	var stuck []string
	for _, c := range commands {
		if inDegree[c.Name] > 0 {
			stuck = append(stuck, c.Name)
		}
	}
	slices.Sort(stuck)
	return fmt.Sprintf("commands involved: %v", stuck)
}

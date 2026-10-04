package compose

import (
	"fmt"
	"maps"
	"slices"
)

// Target addresses a compose command, or one replica of it, as the subject of a
// compose operation.
type Target struct {
	// Command is the compose command name (YAML map key).
	Command string
	// ScaleIndex selects one replica by its 1-based scale index. 0 selects every
	// replica of Command.
	ScaleIndex int
}

// TargetsOf returns one Target per name, each selecting every replica of that
// command.
func TargetsOf(names ...string) []Target {
	if len(names) == 0 {
		return nil
	}
	targets := make([]Target, len(names))
	for i, name := range names {
		targets[i] = Target{Command: name}
	}
	return targets
}

// targetSet is the resolved form of a []Target. Each targeted command maps to
// its selected scale indices in ascending order, or to nil when every replica
// is selected. An empty targetSet targets the whole project.
type targetSet map[string][]int

// resolveTargets validates targets and merges them into a targetSet. replicas
// maps every known compose command to its replica count. A target naming a
// command absent from replicas, or a scale index outside 1..count, is an error.
// Targets on one command merge; a ScaleIndex of 0 among them selects every
// replica.
func resolveTargets(targets []Target, replicas map[string]int) (targetSet, error) {
	if len(targets) == 0 {
		return nil, nil
	}

	var unknown []string
	for _, t := range targets {
		if _, ok := replicas[t.Command]; !ok && !slices.Contains(unknown, t.Command) {
			unknown = append(unknown, t.Command)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, fmt.Errorf("unknown compose command(s): %v", unknown)
	}

	every := make(map[string]bool, len(targets))
	picked := make(map[string]map[int]struct{}, len(targets))
	for _, t := range targets {
		if t.ScaleIndex == 0 {
			every[t.Command] = true
			continue
		}
		count := replicas[t.Command]
		if t.ScaleIndex < 1 || t.ScaleIndex > count {
			return nil, replicaRangeError(t, count)
		}
		if picked[t.Command] == nil {
			picked[t.Command] = make(map[int]struct{})
		}
		picked[t.Command][t.ScaleIndex] = struct{}{}
	}

	set := make(targetSet, len(every)+len(picked))
	for name := range every {
		set[name] = nil
	}
	for name, indices := range picked {
		if !every[name] {
			set[name] = slices.Sorted(maps.Keys(indices))
		}
	}
	return set, nil
}

func replicaRangeError(t Target, count int) error {
	if count == 0 {
		return fmt.Errorf(
			"compose command %q has no replica %d (it has no replicas)", t.Command, t.ScaleIndex)
	}
	return fmt.Errorf(
		"compose command %q has no replica %d (valid range 1..%d)",
		t.Command, t.ScaleIndex, count)
}

// declaredReplicas maps every command of spec to the replica count the spec
// declares for it.
func declaredReplicas(spec ComposeSpec) map[string]int {
	out := make(map[string]int, len(spec.Commands))
	for _, c := range spec.Commands {
		out[c.Name] = max(c.Scale, 1)
	}
	return out
}

// storedReplicas maps every known compose command to the highest scale index
// among its stored replicas. The known commands are the spec's when spec is
// non-nil, so a declared command with no stored replica maps to 0; otherwise
// they are the commands the entries carry.
//
// The highest index rather than the replica count bounds the range, so a
// replica that exists stays addressable even if a lower one went missing.
func storedReplicas(spec *ComposeSpec, entries []cmdmanEntry) map[string]int {
	out := make(map[string]int)
	if spec != nil {
		for _, c := range spec.Commands {
			out[c.Name] = 0
		}
	}
	for _, e := range entries {
		name := commandNameOf(e)
		if name == "" {
			continue
		}
		count, known := out[name]
		if spec != nil && !known {
			continue
		}
		out[name] = max(count, scaleIndexOf(e))
	}
	return out
}

// names returns the targeted command names in ascending order.
func (ts targetSet) names() []string {
	return slices.Sorted(maps.Keys(ts))
}

// replicaScoped reports whether any targeted command is narrowed to specific
// replicas.
func (ts targetSet) replicaScoped() bool {
	for _, indices := range ts {
		if indices != nil {
			return true
		}
	}
	return false
}

// covers reports whether replica scaleIndex of command falls within its
// selection. A command that ts does not narrow is covered whole, which is what a
// dependency or dependent pulled into a reconcile closure gets.
func (ts targetSet) covers(command string, scaleIndex int) bool {
	indices := ts[command]
	return indices == nil || slices.Contains(indices, scaleIndex)
}

// filter returns the entries ts selects: every entry when ts is empty, and
// otherwise the replicas of targeted commands that fall within their selection.
func (ts targetSet) filter(entries []cmdmanEntry) []cmdmanEntry {
	if len(ts) == 0 {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		name := commandNameOf(e)
		if _, ok := ts[name]; ok && ts.covers(name, scaleIndexOf(e)) {
			out = append(out, e)
		}
	}
	return out
}

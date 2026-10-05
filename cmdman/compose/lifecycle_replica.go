package compose

import (
	"fmt"
	"slices"
)

// hookReplica is the replica a lifecycle event runs for, with what its hook
// commands inherit from it. [Service.specHookReplica] builds it from the spec,
// which is all there is before the replica is created; [storedHookReplica]
// builds it from the stored replica.
type hookReplica struct {
	Project    string
	WorkDir    string
	Command    string
	ScaleIndex int
	Scale      int
	// Name is the replica's cmdman command name. Intermediates are named after
	// it.
	Name string
	// Display labels the replica in progress events.
	Display string
	// Dir and Env are the replica's working directory and resolved
	// environment.
	Dir string
	Env []string
}

// specHookReplica describes replica scaleIndex of nc as [Service.Create] would
// create it. Dir and Env are resolved the way cmdman resolves a create request
// (see buildCreateRequest), minus the cmdman context variables of the replica,
// which has no id yet.
func (s *Service) specHookReplica(spec ComposeSpec, nc Command, scaleIndex int) hookReplica {
	cfg := s.svc.Config()
	dir := nc.Dir
	if dir == "" {
		dir = cfg.DefaultWorkingDir
	}
	var env []string
	if nc.ImportHostEnv {
		env = append(env, cfg.DefaultEnvironment...)
	}
	env = append(env, nc.Env...)
	env = append(
		env,
		composeContextEnv(spec.Project, spec.WorkDir, nc.Name, scaleIndex, nc.Scale)...)
	return hookReplica{
		Project:    spec.Project,
		WorkDir:    spec.WorkDir,
		Command:    nc.Name,
		ScaleIndex: scaleIndex,
		Scale:      max(nc.Scale, 1),
		Name:       InstanceName(nc.GeneratedName, scaleIndex),
		Display:    instanceDisplayName(nc, scaleIndex),
		Dir:        dir,
		Env:        env,
	}
}

// storedHookReplica describes the stored replica e and returns the hooks
// recorded on it.
func storedHookReplica(e cmdmanEntry) (hookReplica, []LifecycleHook, error) {
	r, err := storedReplica(e)
	if err != nil {
		return hookReplica{}, nil, err
	}
	hooks, err := storedHooks(e)
	if err != nil {
		return hookReplica{}, nil, err
	}
	return r, hooks, nil
}

// storedReplica describes the stored replica e, whatever its stored hooks.
func storedReplica(e cmdmanEntry) (hookReplica, error) {
	if e.ConfigJSON == nil {
		return hookReplica{}, fmt.Errorf("command %q has no stored config", e.Name)
	}
	labels := e.ConfigJSON.Labels
	index, scale := ScaleOf(labels)
	return hookReplica{
		Project:    labels[LabelProject],
		WorkDir:    labels[LabelWorkdir],
		Command:    labels[LabelCommand],
		ScaleIndex: index,
		Scale:      max(scale, 1),
		Name:       e.Name,
		Display:    entryDisplayName(e),
		Dir:        e.ConfigJSON.Dir,
		Env:        slices.Clone(e.ConfigJSON.Env),
	}, nil
}

// storedHooks decodes the hooks recorded on the stored replica e, which
// [storedReplica] has described.
func storedHooks(e cmdmanEntry) ([]LifecycleHook, error) {
	hooks, err := decodeHooksLabel(e.ConfigJSON.Labels[LabelHooks])
	if err != nil {
		return nil, fmt.Errorf("command %q: %w", e.Name, err)
	}
	return hooks, nil
}

func (r hookReplica) resourceRef(key string) resourceRef {
	return resourceRef{
		Project:    r.Project,
		WorkDir:    r.WorkDir,
		Command:    r.Command,
		ScaleIndex: r.ScaleIndex,
		Key:        key,
	}
}

package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
)

// Labels of intermediate commands: the cmdman commands a replica's lifecycle
// hooks create besides the replica itself. An exec command runs one hook event
// and is named by [ExecCommandName]; a resource holder stores one resource
// value, is never started, and is named by [HolderName].
//
// Intermediates never carry [LabelProject] or [LabelWorkdir], by which every
// compose verb selects the commands of a project, so no verb mistakes one for a
// replica. They name their project with [LabelHooksProject] and
// [LabelHooksWorkdir] instead.
const (
	// LabelIntermediate is [IntermediateExec] or [IntermediateHolder].
	LabelIntermediate = "cmdman.compose.intermediate"
	// LabelOwner is the cmdman command name of the replica the intermediate
	// was created for.
	LabelOwner = "cmdman.compose.owner"
	// LabelHooksProject is the compose project the owning replica belongs to.
	LabelHooksProject = "cmdman.compose.hooks.project"
	// LabelHooksWorkdir is the canonical work directory of that project.
	LabelHooksWorkdir = "cmdman.compose.hooks.workdir"

	// LabelHook is the name of the hook item an exec command runs.
	LabelHook = "cmdman.compose.hook"
	// LabelHookEvent is the [LifecycleEvent] an exec command runs.
	LabelHookEvent = "cmdman.compose.hook-event"

	// LabelResourceKey is the resource key a holder stores the value of.
	LabelResourceKey = "cmdman.compose.resource.key"
	// LabelResourceValue is the stored resource value.
	LabelResourceValue = "cmdman.compose.resource.value"
	// LabelResourceCommand is the compose command name of the owning replica.
	LabelResourceCommand = "cmdman.compose.resource.command"
	// LabelResourceScaleIndex is the 1-based scale index of the owning replica.
	LabelResourceScaleIndex = "cmdman.compose.resource.scale-index"
	// LabelResourceRelease is the hook event that releases the resource, as a
	// JSON object {"event", "args", "on_error"}. It is absent when nothing
	// releases the resource.
	LabelResourceRelease = "cmdman.compose.resource.release"
)

// Values of [LabelIntermediate].
const (
	IntermediateExec   = "exec"
	IntermediateHolder = "holder"
)

// execLabels returns the labels of the exec command that runs hook's ev for r.
// They also select the exec commands an earlier run of the same event left.
func execLabels(r hookReplica, hook string, ev LifecycleEvent) map[string]string {
	return map[string]string{
		LabelIntermediate: IntermediateExec,
		LabelOwner:        r.Name,
		LabelHooksProject: r.Project,
		LabelHooksWorkdir: r.WorkDir,
		LabelHook:         hook,
		LabelHookEvent:    string(ev),
	}
}

// resourceRef addresses one resource of one replica by what its holder
// records, so a holder stays reachable once its replica is gone.
type resourceRef struct {
	Project    string
	WorkDir    string
	Command    string
	ScaleIndex int
	Key        string
}

// selector returns the labels that find the holder of r.
func (r resourceRef) selector() map[string]string {
	return map[string]string{
		LabelIntermediate:       IntermediateHolder,
		LabelHooksProject:       r.Project,
		LabelHooksWorkdir:       r.WorkDir,
		LabelResourceCommand:    r.Command,
		LabelResourceScaleIndex: strconv.Itoa(r.ScaleIndex),
		LabelResourceKey:        r.Key,
	}
}

// replicaName returns the cmdman command name of the replica r belongs to,
// derived the way [Normalize] and [InstanceName] derive it.
func (r resourceRef) replicaName() string {
	return InstanceName(GenerateName(workdirHash(r.WorkDir), r.Project, r.Command), r.ScaleIndex)
}

// resourceRelease is the [LabelResourceRelease] value.
type resourceRelease struct {
	Event   LifecycleEvent `json:"event"`
	Args    []string       `json:"args"`
	OnError OnError        `json:"on_error"`
}

// resourceHolder is the content of one holder.
type resourceHolder struct {
	Ref resourceRef
	// Owner is the cmdman command name of the owning replica.
	Owner   string
	Value   string
	Release *resourceRelease
	// Dir and Env are what the release hook runs with.
	Dir string
	Env []string
}

func (h resourceHolder) name() string {
	return HolderName(h.Owner, h.Ref.Key)
}

func (h resourceHolder) labels() (map[string]string, error) {
	labels := h.Ref.selector()
	labels[LabelOwner] = h.Owner
	labels[LabelResourceValue] = h.Value
	if h.Release != nil {
		release, err := json.Marshal(h.Release)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", LabelResourceRelease, err)
		}
		labels[LabelResourceRelease] = string(release)
	}
	return labels, nil
}

// createRequest returns the request that creates h, or replaces the holder
// already named like it. cmdman commits a replacement only once every step of
// it succeeded, so a failed one leaves the previous holder in place.
func (h resourceHolder) createRequest() (cmdman.CreateRequest, error) {
	labels, err := h.labels()
	if err != nil {
		return cmdman.CreateRequest{}, err
	}
	// The holder is never started. Env is the release hook's environment, kept
	// as given so that it reaches the exec command which eventually runs the
	// release; that command gets its own cmdman context variables.
	off := false
	return cmdman.CreateRequest{
		Name:          h.name(),
		Dir:           h.Dir,
		Env:           h.Env,
		ImportHostEnv: &off,
		InjectEnv:     &off,
		Argv:          []string{"true"},
		LogDriver:     logdriver.DriverNone,
		RestartPolicy: model.RestartPolicyNo,
		Labels:        labels,
		Replace:       true,
	}, nil
}

// decodeHolder reads a holder back from its stored entry.
func decodeHolder(e cmdmanEntry) (resourceHolder, error) {
	if e.ConfigJSON == nil {
		return resourceHolder{}, fmt.Errorf("resource holder %q has no stored config", e.Name)
	}
	labels := e.ConfigJSON.Labels
	index, err := strconv.Atoi(labels[LabelResourceScaleIndex])
	if err != nil {
		return resourceHolder{}, fmt.Errorf(
			"resource holder %q: %s: %w", e.Name, LabelResourceScaleIndex, err)
	}
	h := resourceHolder{
		Ref: resourceRef{
			Project:    labels[LabelHooksProject],
			WorkDir:    labels[LabelHooksWorkdir],
			Command:    labels[LabelResourceCommand],
			ScaleIndex: index,
			Key:        labels[LabelResourceKey],
		},
		Owner: labels[LabelOwner],
		Value: labels[LabelResourceValue],
		Dir:   e.ConfigJSON.Dir,
		Env:   e.ConfigJSON.Env,
	}
	if raw := labels[LabelResourceRelease]; raw != "" {
		var release resourceRelease
		if err := json.Unmarshal([]byte(raw), &release); err != nil {
			return resourceHolder{}, fmt.Errorf(
				"resource holder %q: decode %s: %w", e.Name, LabelResourceRelease, err)
		}
		h.Release = &release
	}
	return h, nil
}

// findHolder returns the holder of ref, or nil when there is none.
func (s *Service) findHolder(ctx context.Context, ref resourceRef) (*cmdmanEntry, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{AllStates: true, Labels: ref.selector()})
	if err != nil {
		return nil, fmt.Errorf("look up holder of resource %q: %w", ref.Key, err)
	}
	switch len(entries) {
	case 0:
		return nil, nil
	case 1:
		return &entries[0], nil
	default:
		return nil, fmt.Errorf(
			"resource %q of %s replica %d has %d holders",
			ref.Key, ref.Command, ref.ScaleIndex, len(entries),
		)
	}
}

// putHolder creates h, or replaces the holder that already has its name.
func (s *Service) putHolder(ctx context.Context, h resourceHolder) error {
	req, err := h.createRequest()
	if err != nil {
		return err
	}
	if _, err := s.svc.Create(ctx, req); err != nil {
		return fmt.Errorf("store resource %q in %s: %w", h.Ref.Key, h.name(), err)
	}
	return nil
}

// dropHolder removes the holder of ref, if there is one.
func (s *Service) dropHolder(ctx context.Context, ref resourceRef) error {
	holder, err := s.findHolder(ctx, ref)
	if err != nil || holder == nil {
		return err
	}
	if err := s.removeIntermediate(ctx, holder.ID); err != nil {
		return fmt.Errorf("remove holder %s of resource %q: %w", holder.Name, ref.Key, err)
	}
	return nil
}

// removeIntermediate removes the cmdman command id. It refuses one that is
// running, so a live process never loses its monitor's record.
func (s *Service) removeIntermediate(ctx context.Context, id string) error {
	results, err := s.svc.Remove(ctx, cmdman.RemoveRequest{Targets: []string{id}})
	if err != nil {
		return err
	}
	for _, r := range results {
		if r.Err != nil {
			return r.Err
		}
	}
	return nil
}

// clearStaleExecs stops and removes the exec commands an earlier run of the
// same hook event left, as selected by labels, so the name is free again and no
// earlier run is still going when the next one starts.
func (s *Service) clearStaleExecs(ctx context.Context, labels map[string]string) error {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{AllStates: true, Labels: labels})
	if err != nil {
		return fmt.Errorf("look up earlier hook commands: %w", err)
	}
	for _, e := range entries {
		if err := s.stopForRecreate(ctx, e.ID); err != nil {
			return fmt.Errorf("stop earlier hook command %s: %w", e.Name, err)
		}
		if err := s.removeIntermediate(ctx, e.ID); err != nil {
			return fmt.Errorf("remove earlier hook command %s: %w", e.Name, err)
		}
	}
	return nil
}

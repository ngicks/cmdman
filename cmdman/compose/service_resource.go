package compose

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/ngicks/cmdman/cmdman"
)

// ErrNoResource reports that no holder stores the requested resource.
var ErrNoResource = errors.New("no such resource")

// ResourceOption addresses one resource of one replica of a compose command.
type ResourceOption struct {
	// Command is the compose command name (YAML map key).
	Command string
	// ScaleIndex selects the replica by its 1-based scale index. 0 selects the
	// sole replica of a command that has one, and is an error for a command
	// with more. With no replica known at all, 0 selects the one replica the
	// resource is held for, or replica 1 when it is held for none.
	ScaleIndex int
	// Key is the resource key.
	Key string
}

// ResourceGet returns the value of a resource. It returns an error wrapping
// [ErrNoResource] when no holder stores it.
//
// The holder is found by the project, command, scale index and key it
// records, so the value stays readable once its replica is gone.
func (s *Service) ResourceGet(
	ctx context.Context,
	selection ProjectSelection,
	opts ResourceOption,
) (string, error) {
	target, err := s.resolveResource(ctx, selection, opts)
	if err != nil {
		return "", err
	}
	if target.holder == nil {
		return "", target.missing()
	}
	holder, err := decodeHolder(*target.holder)
	if err != nil {
		return "", err
	}
	return holder.Value, nil
}

// ResourceSet stores value as the value of a resource, creating its holder or
// replacing the one there is. A replacement keeps the release hook and
// environment the holder already has; a holder created here records no
// release. A failed replacement leaves the previous value in place.
func (s *Service) ResourceSet(
	ctx context.Context,
	selection ProjectSelection,
	opts ResourceOption,
	value string,
) error {
	target, err := s.resolveResource(ctx, selection, opts)
	if err != nil {
		return err
	}
	holder := resourceHolder{
		Ref:   target.ref,
		Owner: target.ref.replicaName(),
		Dir:   selection.WorkDir,
		Env: append(
			composeContextEnv(
				target.ref.Project, target.ref.WorkDir, target.ref.Command,
				target.ref.ScaleIndex, target.scale,
			),
			ENV_CMDMAN_COMPOSE_RESOURCE_KEY+"="+target.ref.Key,
		),
	}
	if target.holder != nil {
		holder, err = decodeHolder(*target.holder)
		if err != nil {
			return err
		}
	}
	holder.Value = value
	return s.putHolder(ctx, holder)
}

// ResourceUnset removes a resource. Removing one that does not exist is not an
// error.
func (s *Service) ResourceUnset(
	ctx context.Context,
	selection ProjectSelection,
	opts ResourceOption,
) error {
	target, err := s.resolveResource(ctx, selection, opts)
	if err != nil {
		return err
	}
	if target.holder == nil {
		return nil
	}
	if err := s.removeIntermediate(ctx, target.holder.ID); err != nil {
		return fmt.Errorf("remove holder %s of resource %q: %w",
			target.holder.Name, opts.Key, err)
	}
	return nil
}

// resourceTarget is a resolved [ResourceOption].
type resourceTarget struct {
	ref resourceRef
	// scale is the replica count known for the command, at least 1.
	scale int
	// holder is the holder of ref, or nil when there is none.
	holder *cmdmanEntry
}

func (t resourceTarget) missing() error {
	return fmt.Errorf("resource %q of compose command %q replica %d: %w",
		t.ref.Key, t.ref.Command, t.ref.ScaleIndex, ErrNoResource)
}

// resolveResource resolves opts within selection. The replica count comes
// from the spec when it declares the command and from the stored replicas
// otherwise; the holders of the key stand in when neither knows a replica.
func (s *Service) resolveResource(
	ctx context.Context,
	selection ProjectSelection,
	opts ResourceOption,
) (resourceTarget, error) {
	if selection.Project == "" {
		return resourceTarget{}, errors.New(
			"compose resource: no project selected; load a compose file or name the project")
	}
	if err := validateName("command", opts.Command); err != nil {
		return resourceTarget{}, fmt.Errorf("compose resource: %w", err)
	}
	if err := validateName("resource", opts.Key); err != nil {
		return resourceTarget{}, fmt.Errorf("compose resource: %w", err)
	}
	if opts.ScaleIndex < 0 {
		return resourceTarget{}, fmt.Errorf(
			"compose resource: scale index must be 1 or greater, got %d", opts.ScaleIndex)
	}

	replicas, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return resourceTarget{}, fmt.Errorf("list project commands: %w", err)
	}
	count := knownReplicas(selection.Spec, replicas, opts.Command)

	keyRef := resourceRef{
		Project: selection.Project,
		WorkDir: selection.WorkDir,
		Command: opts.Command,
		Key:     opts.Key,
	}
	selector := keyRef.selector()
	delete(selector, LabelResourceScaleIndex)
	holders, err := s.svc.List(ctx, cmdman.ListRequest{AllStates: true, Labels: selector})
	if err != nil {
		return resourceTarget{}, fmt.Errorf("look up holders of resource %q: %w", opts.Key, err)
	}
	byIndex := make(map[int][]cmdmanEntry, len(holders))
	for _, h := range holders {
		decoded, err := decodeHolder(h)
		if err != nil {
			return resourceTarget{}, err
		}
		byIndex[decoded.Ref.ScaleIndex] = append(byIndex[decoded.Ref.ScaleIndex], h)
	}

	index, err := resourceScaleIndex(opts, count, slices.Sorted(maps.Keys(byIndex)))
	if err != nil {
		return resourceTarget{}, err
	}
	keyRef.ScaleIndex = index
	target := resourceTarget{ref: keyRef, scale: max(count, 1)}
	switch held := byIndex[index]; len(held) {
	case 0:
	case 1:
		target.holder = &held[0]
	default:
		return resourceTarget{}, fmt.Errorf(
			"resource %q of compose command %q replica %d has %d holders",
			opts.Key, opts.Command, index, len(held))
	}
	return target, nil
}

// knownReplicas returns the replica count of command: the scale spec declares
// for it, or else the highest scale index among its stored replicas. It returns
// 0 when neither knows the command.
func knownReplicas(spec *ComposeSpec, replicas []cmdmanEntry, command string) int {
	if spec != nil {
		for _, c := range spec.Commands {
			if c.Name == command {
				return max(c.Scale, 1)
			}
		}
	}
	count := 0
	for _, e := range replicas {
		if commandNameOf(e) == command {
			count = max(count, scaleIndexOf(e))
		}
	}
	return count
}

// resourceScaleIndex picks the replica opts addresses. count is the known
// replica count of the command and heldBy the scale indices the resource has
// holders for, in ascending order.
func resourceScaleIndex(opts ResourceOption, count int, heldBy []int) (int, error) {
	if opts.ScaleIndex > 0 {
		return opts.ScaleIndex, nil
	}
	switch {
	case count > 1:
		return 0, fmt.Errorf(
			"compose command %q runs %d replicas; pick one with --scale 1..%d",
			opts.Command, count, count)
	case count == 1:
		return 1, nil
	}
	switch len(heldBy) {
	case 0:
		return 1, nil
	case 1:
		return heldBy[0], nil
	default:
		return 0, fmt.Errorf(
			"resource %q of compose command %q is held for replicas %v; pick one with --scale",
			opts.Key, opts.Command, heldBy)
	}
}

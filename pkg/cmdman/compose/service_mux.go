package compose

import (
	"context"
	"fmt"
	"slices"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

// MuxLeafResolver builds the leaf resolver and replica counter that `compose
// mux` hands to [mux.Build], backed by a single listing of the project's
// commands. A leaf name is a compose command (service) name; the resolver maps
// (service, scaleIndex) to the backing cmdman command ID and the counter
// reports the service's replica count so an unpinned leaf can cycle.
func (s *Service) MuxLeafResolver(
	ctx context.Context,
	selection ProjectSelection,
) (mux.Resolver, mux.ReplicaCounter, error) {
	byCommand, err := s.projectReplicaIDs(ctx, selection)
	if err != nil {
		return nil, nil, err
	}

	notFound := func(leaf string) error {
		return fmt.Errorf(
			"compose command %q not found in project (has it been created?)", leaf)
	}
	resolver := func(_ context.Context, leaf string, scaleIndex int) (string, error) {
		ids := byCommand[leaf]
		if len(ids) == 0 {
			return "", notFound(leaf)
		}
		if scaleIndex <= 0 {
			if len(ids) != 1 {
				return "", fmt.Errorf(
					"compose command %q has %d replicas; pin a scale index in the layout",
					leaf, len(ids))
			}
			return ids[0], nil
		}
		if scaleIndex > len(ids) {
			return "", fmt.Errorf(
				"compose command %q has no replica %d (scale is %d)",
				leaf, scaleIndex, len(ids))
		}
		return ids[scaleIndex-1], nil
	}
	counter := func(_ context.Context, leaf string) (int, error) {
		ids := byCommand[leaf]
		if len(ids) == 0 {
			return 0, notFound(leaf)
		}
		return len(ids), nil
	}
	return resolver, counter, nil
}

// projectReplicaIDs lists the selected project's commands and returns, per
// compose command name, the backing cmdman IDs ordered by 1-based scale index.
func (s *Service) projectReplicaIDs(
	ctx context.Context,
	selection ProjectSelection,
) (map[string][]string, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	type idIdx struct {
		id  string
		idx int
	}
	grouped := make(map[string][]idIdx)
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		name := e.ConfigJSON.Labels[LabelCommand]
		if name == "" {
			continue
		}
		grouped[name] = append(grouped[name], idIdx{id: e.ID, idx: scaleIndexOf(e)})
	}
	out := make(map[string][]string, len(grouped))
	for name, items := range grouped {
		slices.SortFunc(items, func(a, b idIdx) int { return a.idx - b.idx })
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = it.id
		}
		out[name] = ids
	}
	return out, nil
}

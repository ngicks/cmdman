package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

// RestartOption configures a Restart operation.
type RestartOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
}

// RestartResult is the aggregated result of a compose restart operation.
type RestartResult struct {
	Restarts []RestartOutcome
}

// RestartOutcome records the result of restarting a single compose command.
type RestartOutcome struct {
	Command  string
	StopErr  error
	StartErr error
}

// Restart stops then starts project-labeled commands.
//
// When Spec is loaded:
//   - Stop phase: reverse DAG order (dependents before dependencies), concurrent within each layer.
//   - Start phase: forward DAG order (matching up), concurrent within each layer.
//   - Orphans (project-labeled commands absent from YAML) are skipped with a warning,
//     consistent with create/up convergence semantics.
//
// When no Spec is loaded, the dependency graph is reconstructed from stored
// compose labels.
//
// Per resolved-decision 21, failures are aggregated; every command is attempted.
func (s *Service) Restart(
	ctx context.Context,
	selection ProjectSelection,
	opts RestartOption,
) (*RestartResult, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	if err := validateCommandNames(opts.CommandNames, selection.Spec, entries); err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn(
			"compose restart: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "restart",
		)
		return &RestartResult{}, nil
	}

	if selection.Spec != nil {
		if len(opts.CommandNames) > 0 {
			entries = filterByCommandNames(entries, opts.CommandNames)
		}
		return s.restartWithSpec(ctx, selection, entries)
	}
	spec, ok, err := reconstructProjectFromMeta(selection, entries)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf(
			"compose restart: stored dependency graph is ambiguous; pass -f or --project-name",
		)
	}
	selection.Spec = &spec
	if len(opts.CommandNames) > 0 {
		entries = filterByCommandNames(entries, opts.CommandNames)
	}
	return s.restartWithSpec(ctx, selection, entries)
}

// restartWithSpec restarts using DAG ordering (reverse stop, forward start).
func (s *Service) restartWithSpec(
	ctx context.Context,
	selection ProjectSelection,
	entries []cmdmanEntry,
) (*RestartResult, error) {
	layers, err := TopoLayers(selection.Spec.Commands)
	if err != nil {
		return nil, fmt.Errorf("topo layers: %w", err)
	}

	idsByCommand := buildIDsByCommand(entries)
	genNamesByCommand := buildGenNamesByCommand(entries)

	// Identify orphan entries (in project labels but not in YAML).
	yamlNames := make(map[string]struct{}, len(selection.Spec.Commands))
	for _, nc := range selection.Spec.Commands {
		yamlNames[nc.Name] = struct{}{}
	}
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		cn := e.ConfigJSON.Labels[LabelCommand]
		if _, inYAML := yamlNames[cn]; !inYAML {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose restart: skipping orphan command",
				"project", selection.Project,
				"workdir", selection.WorkDir,
				"command", cn,
				"id", e.ID,
			)
		}
	}

	// Stop phase: reverse DAG order.
	stopLayers := make([][]string, len(layers))
	copy(stopLayers, layers)
	reverseLayers(stopLayers)

	outByCommand := make(map[string]*RestartOutcome)
	for _, nc := range selection.Spec.Commands {
		outByCommand[nc.Name] = &RestartOutcome{Command: nc.Name}
	}

	for _, layer := range stopLayers {
		stopLayerRestartConcurrent(
			ctx,
			s,
			layer,
			idsByCommand,
			outByCommand,
			selection.Project,
			true,
		)
	}

	// Start phase: forward DAG order.
	for _, layer := range layers {
		startLayerRestartConcurrent(
			ctx,
			s,
			layer,
			genNamesByCommand,
			outByCommand,
			selection.Project,
		)
	}

	// Collect results in stable order (same order as YAML/topo-sorted).
	var restarts []RestartOutcome
	for _, layer := range layers {
		for _, name := range layer {
			if o, ok := outByCommand[name]; ok {
				restarts = append(restarts, *o)
			}
		}
	}

	return &RestartResult{Restarts: restarts}, nil
}

// stopLayerRestartConcurrent stops a layer for the restart operation, recording
// results into outByCommand. Every replica of each command is stopped; the
// command's outcome records the first stop error across its replicas.
func stopLayerRestartConcurrent(
	ctx context.Context,
	s *Service,
	layer []string,
	idsByCommand map[string][]string,
	outByCommand map[string]*RestartOutcome,
	project string,
	_ bool, // reserved for future use
) {
	var mu sync.Mutex
	eg, _ := errgroup.WithContext(ctx)

	for _, name := range layer {
		for _, id := range idsByCommand[name] {
			eg.Go(func() error {
				_, stopErr := s.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}})
				if stopErr != nil {
					contextkey.ValueSlogLoggerDefault(ctx).Warn("compose restart: stop failed",
						"project", project,
						"command", name,
						"id", id,
						"error", stopErr,
					)
				}
				mu.Lock()
				if o, ok := outByCommand[name]; ok && stopErr != nil && o.StopErr == nil {
					o.StopErr = stopErr
				}
				mu.Unlock()
				return nil
			})
		}
	}
	_ = eg.Wait()
}

// startLayerRestartConcurrent starts a layer for the restart operation,
// recording results into outByCommand. Every replica of each command is
// started; the command's outcome records the first start error.
func startLayerRestartConcurrent(
	ctx context.Context,
	s *Service,
	layer []string,
	genNamesByCommand map[string][]string,
	outByCommand map[string]*RestartOutcome,
	project string,
) {
	var mu sync.Mutex
	eg, _ := errgroup.WithContext(ctx)

	for _, name := range layer {
		for _, genName := range genNamesByCommand[name] {
			eg.Go(func() error {
				// Idempotency: if already running/starting, skip.
				startErr := s.svc.Start(ctx, genName)
				if startErr != nil {
					contextkey.ValueSlogLoggerDefault(ctx).Warn("compose restart: start failed",
						"project", project,
						"command", name,
						"generated_name", genName,
						"error", startErr,
					)
				}
				mu.Lock()
				if o, ok := outByCommand[name]; ok && startErr != nil && o.StartErr == nil {
					o.StartErr = startErr
				}
				mu.Unlock()
				return nil
			})
		}
	}
	_ = eg.Wait()
}

// buildGenNamesByCommand groups the existing entries' cmdman names by their
// compose command name, so every replica of a command is restarted.
func buildGenNamesByCommand(entries []cmdmanEntry) map[string][]string {
	m := make(map[string][]string, len(entries))
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		name := e.ConfigJSON.Labels[LabelCommand]
		if name == "" {
			continue
		}
		m[name] = append(m[name], e.Name)
	}
	return m
}

// idleStates are the states where a command is not active and can be started.

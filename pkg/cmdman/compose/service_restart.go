package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
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
// When no Spec is loaded:
//   - Both stop and start phases run fully concurrently.
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
	if len(opts.CommandNames) > 0 {
		entries = filterByCommandNames(entries, opts.CommandNames)
	}

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose restart: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "restart",
		)
		return &RestartResult{}, nil
	}

	if selection.Spec != nil {
		return s.restartWithSpec(ctx, selection, entries)
	}
	return s.restartWithoutSpec(ctx, selection, entries)
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

	idByCommand := buildIDByCommand(entries)
	genNameByCommand := buildGenNameByCommand(selection.Spec.Commands)

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
			idByCommand,
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
			genNameByCommand,
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

// restartWithoutSpec restarts all entries concurrently (both stop and start phases).
func (s *Service) restartWithoutSpec(
	ctx context.Context,
	selection ProjectSelection,
	entries []cmdmanEntry,
) (*RestartResult, error) {
	outByID := make(map[string]*RestartOutcome, len(entries))
	nameByID := make(map[string]string, len(entries))
	for _, e := range entries {
		name := ""
		if e.ConfigJSON != nil {
			name = e.ConfigJSON.Labels[LabelCommand]
		}
		outByID[e.ID] = &RestartOutcome{Command: name}
		nameByID[e.ID] = name
	}

	// Stop phase: concurrent.
	var (
		stopMu sync.Mutex
	)
	stopEg, _ := errgroup.WithContext(ctx)
	for _, entry := range entries {
		id := entry.ID
		name := nameByID[id]
		stopEg.Go(func() error {
			_, stopErr := s.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}})
			if stopErr != nil {
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose restart: stop failed",
					"project", selection.Project,
					"command", name,
					"id", id,
					"error", stopErr,
				)
			}
			stopMu.Lock()
			outByID[id].StopErr = stopErr
			stopMu.Unlock()
			return nil
		})
	}
	_ = stopEg.Wait()

	// Start phase: concurrent.
	var (
		startMu sync.Mutex
	)
	startEg, _ := errgroup.WithContext(ctx)
	for _, entry := range entries {
		id := entry.ID
		name := entry.Name // generated name for cmdman.Service.Start
		cmdName := nameByID[id]
		startEg.Go(func() error {
			startErr := s.svc.Start(ctx, name)
			if startErr != nil {
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose restart: start failed",
					"project", selection.Project,
					"command", cmdName,
					"id", id,
					"error", startErr,
				)
			}
			startMu.Lock()
			outByID[id].StartErr = startErr
			startMu.Unlock()
			return nil
		})
	}
	_ = startEg.Wait()

	var restarts []RestartOutcome
	for _, entry := range entries {
		restarts = append(restarts, *outByID[entry.ID])
	}
	return &RestartResult{Restarts: restarts}, nil
}

// stopLayerRestartConcurrent stops a layer for the restart operation, recording results into
// outByCommand.
func stopLayerRestartConcurrent(
	ctx context.Context,
	s *Service,
	layer []string,
	idByCommand map[string]string,
	outByCommand map[string]*RestartOutcome,
	project string,
	_ bool, // reserved for future use
) {
	var mu sync.Mutex
	eg, _ := errgroup.WithContext(ctx)

	for _, name := range layer {
		id, ok := idByCommand[name]
		if !ok {
			continue
		}
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
			if o, ok := outByCommand[name]; ok {
				o.StopErr = stopErr
			}
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()
}

// startLayerRestartConcurrent starts a layer for the restart operation, recording results into
// outByCommand.
func startLayerRestartConcurrent(
	ctx context.Context,
	s *Service,
	layer []string,
	genNameByCommand map[string]string,
	outByCommand map[string]*RestartOutcome,
	project string,
) {
	var mu sync.Mutex
	eg, _ := errgroup.WithContext(ctx)

	for _, name := range layer {
		genName, ok := genNameByCommand[name]
		if !ok {
			continue
		}
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
			if o, ok := outByCommand[name]; ok {
				o.StartErr = startErr
			}
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()
}

// buildGenNameByCommand returns a map from compose command name to its GeneratedName.
func buildGenNameByCommand(commands []Command) map[string]string {
	m := make(map[string]string, len(commands))
	for _, nc := range commands {
		m[nc.Name] = nc.GeneratedName
	}
	return m
}

// idleStates are the states where a command is not active and can be started.
var _ = store.StateRunning // ensure store import is used via RestartOutcome

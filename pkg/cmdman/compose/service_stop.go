package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

// StopOption configures a Stop operation.
type StopOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
}

// StopResult is the aggregated result of a compose stop operation.
type StopResult struct {
	Stops []StopOutcome
}

// StopOutcome records the result of stopping a single compose command.
type StopOutcome struct {
	Command string
	Err     error
}

// Stop stops project-labeled commands.
//
// When selection carries a Spec (compose file loaded), commands are stopped in
// reverse topological (DAG) order: dependents before dependencies. Within each
// layer, stops run concurrently via errgroup.
//
// When no Spec is loaded, all selected commands stop concurrently.
//
// An empty resolved target set is not an error: the caller should emit a
// structured-log event and return nil.
//
// Per resolved-decision 21, failures are aggregated; every command is attempted.
func (s *Service) Stop(
	ctx context.Context,
	selection ProjectSelection,
	opts StopOption,
) (*StopResult, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	// Filter by command names when provided.
	if err := validateCommandNames(opts.CommandNames, selection.Spec, entries); err != nil {
		return nil, err
	}
	if len(opts.CommandNames) > 0 {
		entries = filterByCommandNames(entries, opts.CommandNames)
	}

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose stop: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "stop",
		)
		return &StopResult{}, nil
	}

	var stops []StopOutcome

	if selection.Spec != nil {
		// Spec available: reverse DAG order, concurrent within each layer.
		layers, err := TopoLayers(selection.Spec.Commands)
		if err != nil {
			return nil, fmt.Errorf("topo layers: %w", err)
		}
		// Reverse layers: dependents first.
		reverseLayers(layers)

		// Build a lookup from compose command name → ID.
		idByCommand := buildIDByCommand(entries)

		for _, layer := range layers {
			outcomes := stopLayerConcurrent(ctx, s, layer, idByCommand, selection.Project)
			stops = append(stops, outcomes...)
		}
	} else {
		// No spec: all concurrent.
		stops = stopAllConcurrent(ctx, s, entries, selection.Project)
	}

	return &StopResult{Stops: stops}, nil
}

// stopLayerConcurrent stops a single DAG layer concurrently and returns outcomes.
// Commands absent from idByCommand (not in the project's running set) are skipped silently.
func stopLayerConcurrent(
	ctx context.Context,
	s *Service,
	layer []string,
	idByCommand map[string]string,
	project string,
) []StopOutcome {
	var (
		mu       sync.Mutex
		outcomes []StopOutcome
	)
	eg, _ := errgroup.WithContext(ctx)

	for _, name := range layer {
		id, ok := idByCommand[name]
		if !ok {
			// Command is in YAML but not in the running project; skip.
			continue
		}
		eg.Go(func() error {
			_, err := s.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}})
			outcome := StopOutcome{Command: name}
			if err != nil {
				outcome.Err = fmt.Errorf("stop command %q (%s): %w", name, id, err)
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose stop: stop failed",
					"project", project,
					"command", name,
					"id", id,
					"error", err,
				)
			}
			mu.Lock()
			outcomes = append(outcomes, outcome)
			mu.Unlock()
			return nil // always nil — aggregate, never short-circuit
		})
	}
	_ = eg.Wait()
	return outcomes
}

// stopAllConcurrent stops all entries concurrently and returns outcomes.
func stopAllConcurrent(
	ctx context.Context,
	s *Service,
	entries []cmdmanEntry,
	project string,
) []StopOutcome {
	var (
		mu       sync.Mutex
		outcomes []StopOutcome
	)
	eg, _ := errgroup.WithContext(ctx)

	for _, entry := range entries {
		cmdName := ""
		if entry.ConfigJSON != nil {
			cmdName = entry.ConfigJSON.Labels[LabelCommand]
		}
		id := entry.ID
		name := cmdName
		eg.Go(func() error {
			_, err := s.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}})
			outcome := StopOutcome{Command: name}
			if err != nil {
				outcome.Err = fmt.Errorf("stop command %q (%s): %w", name, id, err)
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose stop: stop failed",
					"project", project,
					"command", name,
					"id", id,
					"error", err,
				)
			}
			mu.Lock()
			outcomes = append(outcomes, outcome)
			mu.Unlock()
			return nil
		})
	}
	_ = eg.Wait()
	return outcomes
}

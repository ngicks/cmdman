package compose

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/go-common/contextkey"
)

// RestartOption configures a Restart operation.
type RestartOption struct {
	// Targets optionally narrows the restart to specific compose commands or
	// replicas. Empty targets the whole project. A replica index must name a
	// stored replica.
	Targets []Target
	// Timeout is how long the stop of each replica waits after the stop signal
	// before it sends SIGKILL. Nil waits each replica's stored stop timeout. A
	// non-positive value fails the restart before any replica is stopped.
	Timeout *time.Duration
}

// RestartResult is the aggregated result of a compose restart operation.
type RestartResult struct {
	Restarts []RestartOutcome
}

// RestartOutcome records the result of restarting a single replica of a
// compose command.
type RestartOutcome struct {
	// Command labels the replica: the bare command name for an unscaled command,
	// "<command>-<index>" for a scaled one.
	Command  string
	StopErr  error
	StartErr error
}

// Restart stops then starts project-labeled commands, and reports one outcome
// per replica it restarted.
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
	if err := checkStopTimeout(opts.Timeout); err != nil {
		return nil, err
	}
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	targets, err := resolveTargets(opts.Targets, storedReplicas(selection.Spec, entries))
	if err != nil {
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

	td := &teardown{timeout: opts.Timeout}
	if selection.Spec != nil {
		return s.restartWithSpec(ctx, selection, entries, targets, hooksFromSpec, td)
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
	return s.restartWithSpec(ctx, selection, entries, targets, hooksFromStored, td)
}

// restartWithSpec restarts using DAG ordering (reverse stop, forward start). It
// stops and starts exactly the replicas of entries, every stored replica of the
// project, that targets selects, and reports one outcome per such replica.
//
// A live replica stops inside the stop hooks stored on it, as td says. Every
// replica starts inside the start hooks src says to run for it. A hook that
// fails under on_error fail ends the restart of its replica: one whose stop
// hooks failed is not started.
func (s *Service) restartWithSpec(
	ctx context.Context,
	selection ProjectSelection,
	entries []cmdmanEntry,
	targets targetSet,
	src hookSource,
	td *teardown,
) (*RestartResult, error) {
	layers, err := TopoLayers(selection.Spec.Commands)
	if err != nil {
		return nil, fmt.Errorf("topo layers: %w", err)
	}

	nameOf := replicaNamer(selection.Spec, entries)
	entries = targets.filter(entries)
	entriesByCommand := buildEntriesByCommand(entries)
	cmdByName := make(map[string]Command, len(selection.Spec.Commands))
	for _, nc := range selection.Spec.Commands {
		cmdByName[nc.Name] = nc
	}
	// held collects the IDs of the replicas a failed stop hook leaves out of the
	// start phase. Only the stop phase writes it, and it ends before the start
	// phase reads it.
	held := make(map[string]struct{})

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

	// One outcome per restarted replica, keyed by its ID. An orphan has none: no
	// layer names its command, so it is neither stopped nor started.
	outByID := make(map[string]*RestartOutcome, len(entries))
	for name, replicas := range entriesByCommand {
		if _, inYAML := yamlNames[name]; !inYAML {
			continue
		}
		for _, e := range replicas {
			outByID[e.ID] = &RestartOutcome{Command: nameOf(e)}
		}
	}

	for _, layer := range stopLayers {
		stopLayerRestartConcurrent(
			ctx,
			s,
			layer,
			entriesByCommand,
			outByID,
			held,
			selection.Project,
			td,
		)
	}

	// Start phase: forward DAG order.
	for _, layer := range layers {
		startLayerRestartConcurrent(
			ctx,
			s,
			layer,
			entriesByCommand,
			outByID,
			held,
			selection.Project,
			func(ctx context.Context, e cmdmanEntry) error {
				return s.startReplica(
					ctx, *selection.Spec, src, cmdByName[commandNameOf(e)], e)
			},
		)
	}

	// Collect results in stable order: topo-sorted commands, each by scale index.
	var restarts []RestartOutcome
	for _, layer := range layers {
		for _, name := range layer {
			for _, e := range entriesByCommand[name] {
				restarts = append(restarts, *outByID[e.ID])
			}
		}
	}

	return &RestartResult{Restarts: restarts}, nil
}

// stopLayerRestartConcurrent stops a layer for the restart operation, recording
// each replica's stop error into its outcome in outByID. Every replica
// entriesByCommand holds for each command is stopped, as td says. A replica
// whose stop hooks failed is added to held.
func stopLayerRestartConcurrent(
	ctx context.Context,
	s *Service,
	layer []string,
	entriesByCommand map[string][]cmdmanEntry,
	outByID map[string]*RestartOutcome,
	held map[string]struct{},
	project string,
	td *teardown,
) {
	var mu sync.Mutex
	eg, _ := errgroup.WithContext(ctx)

	for _, name := range layer {
		for _, e := range entriesByCommand[name] {
			eg.Go(func() error {
				startable, stopErr := s.restartStop(ctx, td, e)
				if stopErr != nil {
					contextkey.ValueSlogLoggerDefault(ctx).Warn("compose restart: stop failed",
						"project", project,
						"command", name,
						"id", e.ID,
						"error", stopErr,
					)
				}
				outByID[e.ID].StopErr = stopErr
				if !startable {
					mu.Lock()
					held[e.ID] = struct{}{}
					mu.Unlock()
				}
				return nil
			})
		}
	}
	_ = eg.Wait()
}

// restartStop stops the replica e for a restart. A live replica stops inside
// the stop hooks stored on it, as td says for force and timeout. Any other
// replica counts as stopped already: cmdman cannot stop one that never started,
// which has no monitor, and one that ended has nothing left to stop. startable
// reports whether the restart of e goes on to its start. A failed stop hook
// ends the restart of e. A failed stop alone leaves the start to be tried.
func (s *Service) restartStop(
	ctx context.Context,
	td *teardown,
	e cmdmanEntry,
) (startable bool, err error) {
	if e.State != model.EventTypeRunning && e.State != model.EventTypeStarting {
		return true, nil
	}
	hookFailed, _, err := s.stopReplica(ctx, e, td.force, td.timeout)
	return !hookFailed, err
}

// startLayerRestartConcurrent starts a layer for the restart operation,
// recording each replica's start error into its outcome in outByID. Every
// replica entriesByCommand holds for each command is started by start unless
// held holds it.
func startLayerRestartConcurrent(
	ctx context.Context,
	s *Service,
	layer []string,
	entriesByCommand map[string][]cmdmanEntry,
	outByID map[string]*RestartOutcome,
	held map[string]struct{},
	project string,
	start func(context.Context, cmdmanEntry) error,
) {
	eg, _ := errgroup.WithContext(ctx)

	for _, name := range layer {
		for _, e := range entriesByCommand[name] {
			if _, ok := held[e.ID]; ok {
				continue
			}
			eg.Go(func() error {
				startErr := start(ctx, e)
				if startErr != nil {
					contextkey.ValueSlogLoggerDefault(ctx).Warn("compose restart: start failed",
						"project", project,
						"command", name,
						"generated_name", e.Name,
						"error", startErr,
					)
				}
				outByID[e.ID].StartErr = startErr
				return nil
			})
		}
	}
	_ = eg.Wait()
}

// buildEntriesByCommand groups the existing entries by their compose command
// name, each group in ascending scale index, so every replica in entries is
// restarted.
func buildEntriesByCommand(entries []cmdmanEntry) map[string][]cmdmanEntry {
	m := make(map[string][]cmdmanEntry, len(entries))
	for _, e := range entries {
		if name := commandNameOf(e); name != "" {
			m[name] = append(m[name], e)
		}
	}
	for _, replicas := range m {
		slices.SortFunc(replicas, func(a, b cmdmanEntry) int {
			return scaleIndexOf(a) - scaleIndexOf(b)
		})
	}
	return m
}

// idleStates are the states where a command is not active and can be started.

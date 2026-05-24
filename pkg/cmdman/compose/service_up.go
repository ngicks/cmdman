package compose

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// UpOption configures an Up operation (a Create followed by a Start), so it
// embeds both option sets.
//
// CreateOption and StartOption both carry CommandNames; Up reads the create
// side (opts.CreateOption.CommandNames), so set the same names on both — or just
// the create side — when targeting a subset.
type UpOption struct {
	CreateOption
	StartOption
}

// UpResult is the aggregated result of a compose up operation.
type UpResult struct {
	CreateResult
	Starts []StartOutcome
}

// Up performs idempotent convergence: runs Create then starts the targeted
// commands honoring after.Condition via the DAG-aware concurrent starter.
//
// Per resolved-decision 21, failures are aggregated; remaining commands continue.
func (s *Service) Up(
	ctx context.Context,
	spec ComposeSpec,
	opts UpOption,
) (*UpResult, error) {
	createResult, err := s.Create(ctx, spec, opts.CreateOption)
	if err != nil {
		return nil, err
	}

	stateByCommand, err := s.snapshotProjectStates(ctx, spec.WorkDir, spec.Project)
	if err != nil {
		return nil, err
	}

	restrict := resolveTargetCommands(spec, opts.CreateOption.CommandNames)
	starts := startInDAGOrder(ctx, s.svc, spec, stateByCommand, restrict)

	return &UpResult{
		CreateResult: *createResult,
		Starts:       starts,
	}, nil
}

// snapshotProjectStates fetches the current state of every project-labeled
// command so the DAG starter can apply idempotency and pre-snapshot fast paths.
func (s *Service) snapshotProjectStates(
	ctx context.Context,
	workDir, project string,
) (map[string]model.EventType, error) {
	existing, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels: map[string]string{
			LabelWorkdir: workDir,
			LabelProject: project,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list existing commands before start: %w", err)
	}
	stateByCommand := map[string]model.EventType{}
	for _, e := range existing {
		if e.ConfigJSON == nil {
			continue
		}
		cmdName := e.ConfigJSON.Labels[LabelCommand]
		if cmdName != "" {
			stateByCommand[cmdName] = e.State
		}
	}
	return stateByCommand, nil
}

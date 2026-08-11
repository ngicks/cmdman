package compose

import (
	"context"
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

	starts, err := s.reconcileStart(ctx, spec, opts.CreateOption.CommandNames)
	if err != nil {
		return nil, err
	}

	return &UpResult{
		CreateResult: *createResult,
		Starts:       starts,
	}, nil
}

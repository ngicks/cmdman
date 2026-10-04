package compose

import (
	"context"
)

// UpOption configures an Up operation (a Create followed by a Start), so it
// embeds both option sets.
//
// CreateOption and StartOption both carry Targets; Up reads the create side
// (opts.CreateOption.Targets), so set the same targets on both — or just the
// create side — when targeting a subset. A replica index must lie within the
// scale the spec declares for its command.
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
	targets, err := resolveTargets(opts.CreateOption.Targets, declaredReplicas(spec))
	if err != nil {
		return nil, err
	}

	createResult, err := s.create(ctx, spec, opts.RemoveOrphan, targets)
	if err != nil {
		return nil, err
	}

	starts, err := s.reconcileStart(ctx, spec, targets, hooksFromSpec)
	if err != nil {
		return nil, err
	}

	return &UpResult{
		CreateResult: *createResult,
		Starts:       starts,
	}, nil
}

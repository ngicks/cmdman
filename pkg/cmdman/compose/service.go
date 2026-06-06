package compose

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

var _ cmdmanSvc = (*cmdman.Service)(nil)

// cmdmanSvc is the minimal interface the compose package needs from cmdman.Service.
// Defined here (consumer side) per the small-interface-at-consumer rule.
// *cmdman.Service satisfies this interface.
type cmdmanSvc interface {
	Start(ctx context.Context, idOrName string) error
	Wait(ctx context.Context, req cmdman.WaitRequest) ([]cmdman.WaitResult, error)
	List(ctx context.Context, req cmdman.ListRequest) ([]store.CommandEntry, error)
	Create(ctx context.Context, req cmdman.CreateRequest) (*cmdman.CreateResult, error)
	Remove(ctx context.Context, req cmdman.RemoveRequest) ([]cmdman.RemoveResult, error)
	Stop(ctx context.Context, req cmdman.StopRequest) ([]cmdman.StopResult, error)
	Signal(ctx context.Context, idOrName string, sig int32) error
	Logs(ctx context.Context, req cmdman.LogsRequest) (logdriver.Reader, error)
	Inspect(ctx context.Context, idOrName string) (*cmdman.InspectOutput, error)
	Events(ctx context.Context, req cmdman.EventsRequest) (*cmdman.EventsSubscription, error)
	OpenAttachSession(ctx context.Context, idOrName string) (*cmdman.Session, error)
	SendKeys(ctx context.Context, idOrName string, req cmdman.SendKeysRequest) error
}

// Service wraps a cmdmanSvc with compose-specific reconciliation logic.
// It is testable without the CLI.
type Service struct {
	svc cmdmanSvc
	// reporter receives lifecycle state-trace events during up/start/stop/down.
	// nil disables reporting (see report).
	reporter Reporter
}

// NewService constructs a compose.Service from an existing cmdman.Service.
// Options such as WithReporter customize the service.
func NewService(svc *cmdman.Service, opts ...ServiceOption) *Service {
	s := &Service{svc: svc}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CreateOption configures a Create operation.
type CreateOption struct {
	// RemoveOrphan causes stopped orphan commands to be removed.
	// Running orphans are reported and skipped (resolved-decision 4: no force in v1).
	// Ignored when CommandNames targets a subset.
	RemoveOrphan bool
	// CommandNames optionally narrows the operation to specific compose command
	// names and their transitive after-dependencies. Empty targets every command.
	CommandNames []string
}

// CreateResult is the aggregated result of a compose create operation.
type CreateResult struct {
	Actions []ActionOutcome
}

// ActionOutcome records what happened to a single compose command during a create operation.
type ActionOutcome struct {
	// Command is the compose command name (YAML map key).
	Command string
	// Action is the action taken: "create", "recreate", "unchanged", "skipped", "remove-orphan".
	Action string
	// Err holds a non-nil error when the action failed.
	Err error
}

// Create reconciles the desired spec against existing project-labeled commands:
//  1. Lists existing project-labeled commands.
//  2. Calls ComputePlan. Returns a conflict error if the compose file differs.
//  3. Handles orphans: warns (default) or removes stopped orphans when opts.RemoveOrphan
//     is set. Skipped when opts.CommandNames targets a subset.
//  4. Executes create/recreate/unchanged actions for the targeted commands and
//     aggregates outcomes.
func (s *Service) Create(
	ctx context.Context,
	spec ComposeSpec,
	opts CreateOption,
) (*CreateResult, error) {
	if err := validateCommandNames(opts.CommandNames, &spec, nil); err != nil {
		return nil, err
	}

	existing, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels: map[string]string{
			LabelWorkdir: spec.WorkDir,
			LabelProject: spec.Project,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list existing commands: %w", err)
	}

	plan, err := ComputePlan(spec, existing)
	if err != nil {
		return nil, fmt.Errorf(
			"compute plan for project %q in %q: %w",
			spec.Project,
			spec.WorkDir,
			err,
		)
	}

	targets := resolveTargetCommands(spec, opts.CommandNames)

	var actions []ActionOutcome
	// Orphan handling is a whole-project concern; skip it when a subset is targeted.
	if len(opts.CommandNames) == 0 {
		// Step 4 (action ordering): handle orphans before create/recreate/unchanged.
		orphanOutcomes := s.handleOrphans(ctx, spec, plan.Orphans, opts.RemoveOrphan)
		// Prepend orphan outcomes so they appear first in the summary.
		actions = append(actions, orphanOutcomes...)
	}

	for _, action := range plan.Actions {
		if _, ok := targets[action.Desired.Name]; !ok {
			continue
		}
		outcome, err := s.executeAction(ctx, spec, action)
		if err != nil {
			return nil, err // internal/unexpected error; individual cmd errors are in Err field
		}
		actions = append(actions, outcome)
	}

	return &CreateResult{Actions: actions}, nil
}

// executeAction carries out a single plan action and returns its outcome.
func (s *Service) executeAction(
	ctx context.Context,
	spec ComposeSpec,
	action CommandAction,
) (ActionOutcome, error) {
	nc := action.Desired

	switch action.Kind {
	case ActionUnchanged:
		s.report(nc.Name, PhaseUnchanged, nil, nil)
		return ActionOutcome{Command: nc.Name, Action: "unchanged"}, nil

	case ActionCreate:
		s.report(nc.Name, PhaseCreating, nil, nil)
		req := buildCreateRequest(spec, nc, action.DesiredHash)
		_, err := s.svc.Create(ctx, req)
		if err != nil {
			werr := fmt.Errorf("create command %q (%s): %w", nc.Name, nc.GeneratedName, err)
			s.report(nc.Name, PhaseError, werr, nil)
			return ActionOutcome{Command: nc.Name, Action: "create", Err: werr}, nil
		}
		s.report(nc.Name, PhaseCreated, nil, nil)
		return ActionOutcome{Command: nc.Name, Action: "create"}, nil

	case ActionRecreate:
		existing := action.Existing
		if existing == nil {
			werr := fmt.Errorf("recreate command %q: missing existing entry", nc.Name)
			s.report(nc.Name, PhaseSkipped, werr, nil)
			return ActionOutcome{Command: nc.Name, Action: "skipped", Err: werr}, nil
		}

		// A running/starting command is stopped before it can be removed and
		// recreated. The stop is surfaced as its own stopping → stopped step in the
		// trace so the user sees the command go down before it comes back. A stop
		// failure aborts the recreate (the still-running command must not be
		// removed out from under its live monitor).
		if existing.State == model.EventTypeStarted || existing.State == model.EventTypeStarting {
			contextkey.ValueSlogLoggerDefault(ctx).Info(
				"compose: stopping changed command before recreate",
				"project", spec.Project,
				"command", nc.Name,
				"id", existing.ID,
				"state", existing.State,
			)
			s.report(nc.Name, PhaseStopping, nil, nil)
			if err := s.stopForRecreate(ctx, existing.ID); err != nil {
				werr := fmt.Errorf(
					"stop command %q (%s) for recreate: %w",
					nc.Name,
					existing.ID,
					err,
				)
				s.report(nc.Name, PhaseError, werr, nil)
				return ActionOutcome{Command: nc.Name, Action: "recreate", Err: werr}, nil
			}
			s.report(nc.Name, PhaseStopped, nil, nil)
		}

		s.report(nc.Name, PhaseRecreating, nil, nil)
		// Remove then recreate.
		results, err := s.svc.Remove(ctx, cmdman.RemoveRequest{
			Targets: []string{existing.ID},
		})
		if err != nil {
			werr := fmt.Errorf("remove command %q for recreate: %w", nc.Name, err)
			s.report(nc.Name, PhaseError, werr, nil)
			return ActionOutcome{Command: nc.Name, Action: "recreate", Err: werr}, nil
		}
		for _, r := range results {
			if r.Err != nil {
				werr := fmt.Errorf("remove command %q for recreate: %w", nc.Name, r.Err)
				s.report(nc.Name, PhaseError, werr, nil)
				return ActionOutcome{Command: nc.Name, Action: "recreate", Err: werr}, nil
			}
		}

		req := buildCreateRequest(spec, nc, action.DesiredHash)
		_, err = s.svc.Create(ctx, req)
		if err != nil {
			werr := fmt.Errorf("create command %q after remove: %w", nc.Name, err)
			s.report(nc.Name, PhaseError, werr, nil)
			return ActionOutcome{Command: nc.Name, Action: "recreate", Err: werr}, nil
		}
		s.report(nc.Name, PhaseRecreated, nil, nil)
		return ActionOutcome{Command: nc.Name, Action: "recreate"}, nil

	default:
		return ActionOutcome{}, fmt.Errorf("unknown action kind %q", action.Kind)
	}
}

// stopForRecreate stops a running command and waits for it to terminate so its
// store entry can be safely removed and recreated. It honors the command's
// configured stop signal and the default stop timeout (SIGTERM, then SIGKILL on
// timeout). The first error — from the call itself or any per-target result — is
// returned so the caller can abort the recreate.
func (s *Service) stopForRecreate(ctx context.Context, id string) error {
	results, err := s.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}})
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

// buildCreateRequest constructs a cmdman.CreateRequest from a Command
// and its computed config hash. Reserved compose labels are merged with user labels.
func buildCreateRequest(
	spec ComposeSpec,
	nc Command,
	configHash string,
) cmdman.CreateRequest {
	return cmdman.CreateRequest{
		Name:            nc.GeneratedName,
		Dir:             nc.Dir,
		Argv:            nc.Args,
		Env:             nc.Env,
		RestartPolicy:   nc.RestartPolicy,
		MaxRetries:      nc.MaxRetries,
		StopSignal:      nc.StopSignal,
		Tty:             nc.Tty,
		ScrollbackBytes: nc.ScrollbackBytes,
		LogDriver:       nc.LogDriver,
		LogOpts:         nc.LogOpts,
		AutoRemove:      false, // compose owns lifecycle
		Labels:          BuildLabels(spec, nc, configHash),
	}
}

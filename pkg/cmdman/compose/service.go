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
}

// NewService constructs a compose.Service from an existing cmdman.Service.
func NewService(svc *cmdman.Service) *Service {
	return &Service{svc: svc}
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
		return ActionOutcome{Command: nc.Name, Action: "unchanged"}, nil

	case ActionCreate:
		req := buildCreateRequest(spec, nc, action.DesiredHash)
		_, err := s.svc.Create(ctx, req)
		if err != nil {
			return ActionOutcome{
				Command: nc.Name,
				Action:  "create",
				Err:     fmt.Errorf("create command %q (%s): %w", nc.Name, nc.GeneratedName, err),
			}, nil
		}
		return ActionOutcome{Command: nc.Name, Action: "create"}, nil

	case ActionRecreate:
		existing := action.Existing
		if existing == nil {
			return ActionOutcome{
				Command: nc.Name,
				Action:  "skipped",
				Err:     fmt.Errorf("recreate command %q: missing existing entry", nc.Name),
			}, nil
		}

		// If the command is running/starting, skip it (resolved-decision 4: no force in v1).
		if existing.State == model.EventTypeStarted || existing.State == model.EventTypeStarting {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: changed command is running; skipping recreate",
				"project", spec.Project,
				"command", nc.Name,
				"id", existing.ID,
				"state", existing.State,
			)
			return ActionOutcome{
				Command: nc.Name,
				Action:  "skipped",
				Err: fmt.Errorf(
					"command %q is %s; recreate skipped (stop first)",
					nc.Name,
					existing.State,
				),
			}, nil
		}

		// Remove then recreate.
		results, err := s.svc.Remove(ctx, cmdman.RemoveRequest{
			Targets: []string{existing.ID},
		})
		if err != nil {
			return ActionOutcome{
				Command: nc.Name,
				Action:  "recreate",
				Err:     fmt.Errorf("remove command %q for recreate: %w", nc.Name, err),
			}, nil
		}
		for _, r := range results {
			if r.Err != nil {
				return ActionOutcome{
					Command: nc.Name,
					Action:  "recreate",
					Err:     fmt.Errorf("remove command %q for recreate: %w", nc.Name, r.Err),
				}, nil
			}
		}

		req := buildCreateRequest(spec, nc, action.DesiredHash)
		_, err = s.svc.Create(ctx, req)
		if err != nil {
			return ActionOutcome{
				Command: nc.Name,
				Action:  "recreate",
				Err:     fmt.Errorf("create command %q after remove: %w", nc.Name, err),
			}, nil
		}
		return ActionOutcome{Command: nc.Name, Action: "recreate"}, nil

	default:
		return ActionOutcome{}, fmt.Errorf("unknown action kind %q", action.Kind)
	}
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

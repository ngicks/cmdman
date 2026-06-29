package compose

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

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

	// Surplus replicas from a scale-down are always reconciled away (scoped to the
	// targeted commands), regardless of --remove-orphan.
	actions = append(actions, s.handleExcessReplicas(ctx, spec, plan.ExcessReplicas, targets)...)

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

// executeAction carries out a single plan action (one replica) and returns its
// outcome. disp is the user-facing name for the replica (the bare command name
// for an unscaled command, "<command>-<index>" otherwise).
func (s *Service) executeAction(
	ctx context.Context,
	spec ComposeSpec,
	action CommandAction,
) (ActionOutcome, error) {
	nc := action.Desired
	disp := instanceDisplayName(nc, action.ScaleIndex)
	instName := action.InstanceName

	switch action.Kind {
	case ActionUnchanged:
		s.report(disp, PhaseUnchanged, nil, nil)
		return ActionOutcome{Command: disp, Action: "unchanged"}, nil

	case ActionCreate:
		s.report(disp, PhaseCreating, nil, nil)
		req := buildCreateRequest(spec, nc, action.DesiredHash, instName, action.ScaleIndex)
		_, err := s.svc.Create(ctx, req)
		if err != nil {
			werr := fmt.Errorf("create command %q (%s): %w", disp, instName, err)
			s.report(disp, PhaseError, werr, nil)
			return ActionOutcome{Command: disp, Action: "create", Err: werr}, nil
		}
		s.report(disp, PhaseCreated, nil, nil)
		return ActionOutcome{Command: disp, Action: "create"}, nil

	case ActionRecreate:
		existing := action.Existing
		if existing == nil {
			werr := fmt.Errorf("recreate command %q: missing existing entry", disp)
			s.report(disp, PhaseSkipped, werr, nil)
			return ActionOutcome{Command: disp, Action: "skipped", Err: werr}, nil
		}

		// A running/starting command is stopped before it can be removed and
		// recreated. The stop is surfaced as its own stopping → stopped step in the
		// trace so the user sees the command go down before it comes back. A stop
		// failure aborts the recreate (the still-running command must not be
		// removed out from under its live monitor).
		if existing.State == model.EventTypeRunning || existing.State == model.EventTypeStarting {
			contextkey.ValueSlogLoggerDefault(ctx).Info(
				"compose: stopping changed command before recreate",
				"project", spec.Project,
				"command", disp,
				"id", existing.ID,
				"state", existing.State,
			)
			s.report(disp, PhaseStopping, nil, nil)
			if err := s.stopForRecreate(ctx, existing.ID); err != nil {
				werr := fmt.Errorf(
					"stop command %q (%s) for recreate: %w",
					disp,
					existing.ID,
					err,
				)
				s.report(disp, PhaseError, werr, nil)
				return ActionOutcome{Command: disp, Action: "recreate", Err: werr}, nil
			}
			s.report(disp, PhaseStopped, nil, nil)
		}

		s.report(disp, PhaseRecreating, nil, nil)
		// Remove then recreate.
		results, err := s.svc.Remove(ctx, cmdman.RemoveRequest{
			Targets: []string{existing.ID},
		})
		if err != nil {
			werr := fmt.Errorf("remove command %q for recreate: %w", disp, err)
			s.report(disp, PhaseError, werr, nil)
			return ActionOutcome{Command: disp, Action: "recreate", Err: werr}, nil
		}
		for _, r := range results {
			if r.Err != nil {
				werr := fmt.Errorf("remove command %q for recreate: %w", disp, r.Err)
				s.report(disp, PhaseError, werr, nil)
				return ActionOutcome{Command: disp, Action: "recreate", Err: werr}, nil
			}
		}

		req := buildCreateRequest(spec, nc, action.DesiredHash, instName, action.ScaleIndex)
		_, err = s.svc.Create(ctx, req)
		if err != nil {
			werr := fmt.Errorf("create command %q after remove: %w", disp, err)
			s.report(disp, PhaseError, werr, nil)
			return ActionOutcome{Command: disp, Action: "recreate", Err: werr}, nil
		}
		s.report(disp, PhaseRecreated, nil, nil)
		return ActionOutcome{Command: disp, Action: "recreate"}, nil

	default:
		return ActionOutcome{}, fmt.Errorf("unknown action kind %q", action.Kind)
	}
}

// instanceDisplayName is the user-facing label for one replica: the bare
// command name for an unscaled command (so single-replica output is unchanged),
// and "<command>-<index>" once a command runs more than one replica.
func instanceDisplayName(cmd Command, scaleIndex int) string {
	if cmd.Scale <= 1 {
		return cmd.Name
	}
	return fmt.Sprintf("%s-%d", cmd.Name, scaleIndex)
}

// entryDisplayName is [instanceDisplayName] for a stored replica whose desired
// Command is not in hand (orphan stop, project-only down): it reads the compose
// command name, replica count, and scale index from the entry's reserved labels.
// A single-replica command keeps its bare name; a scaled one gets the
// "<command>-<index>" suffix, so both paths label replicas the same way.
func entryDisplayName(e store.CommandEntry) string {
	if e.ConfigJSON == nil {
		return ""
	}
	name := e.ConfigJSON.Labels[LabelCommand]
	idx := scaleIndexOf(e)
	scale := 1
	if n, err := strconv.Atoi(e.ConfigJSON.Labels[LabelScale]); err == nil {
		scale = n
	}
	if scale <= 1 || idx <= 0 {
		return name
	}
	return fmt.Sprintf("%s-%d", name, idx)
}

// handleExcessReplicas stops (when live) and removes surplus replicas left by a
// scale-down. Only replicas whose command is in the target set are touched, so a
// subset operation never tears down a replica it was not asked about. Each
// removal is reported as its own removing → removed/error step.
func (s *Service) handleExcessReplicas(
	ctx context.Context,
	spec ComposeSpec,
	excess []store.CommandEntry,
	targets map[string]struct{},
) []ActionOutcome {
	var outcomes []ActionOutcome
	for _, e := range excess {
		if e.ConfigJSON == nil {
			continue
		}
		cmdName := e.ConfigJSON.Labels[LabelCommand]
		if _, ok := targets[cmdName]; !ok {
			continue
		}
		disp := fmt.Sprintf("%s-%d", cmdName, scaleIndexOf(e))
		s.report(disp, PhaseRemoving, nil, nil)

		// Stop a live replica before removal so its monitor is not yanked.
		if e.State == model.EventTypeRunning || e.State == model.EventTypeStarting {
			if err := s.stopForRecreate(ctx, e.ID); err != nil {
				werr := fmt.Errorf("stop excess replica %q (%s): %w", disp, e.ID, err)
				s.report(disp, PhaseError, werr, nil)
				outcomes = append(outcomes, ActionOutcome{
					Command: disp, Action: "remove-excess", Err: werr,
				})
				continue
			}
		}

		results, err := s.svc.Remove(ctx, cmdman.RemoveRequest{
			Targets: []string{e.ID},
			Force:   true,
		})
		if err == nil {
			for _, r := range results {
				if r.Err != nil {
					err = r.Err
					break
				}
			}
		}
		if err != nil {
			werr := fmt.Errorf("remove excess replica %q (%s): %w", disp, e.ID, err)
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: remove excess replica failed",
				"project", spec.Project,
				"workdir", spec.WorkDir,
				"command", cmdName,
				"id", e.ID,
				"error", err,
			)
			s.report(disp, PhaseError, werr, nil)
			outcomes = append(outcomes, ActionOutcome{
				Command: disp, Action: "remove-excess", Err: werr,
			})
			continue
		}
		s.report(disp, PhaseRemoved, nil, nil)
		outcomes = append(outcomes, ActionOutcome{Command: disp, Action: "remove-excess"})
	}
	return outcomes
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

// buildCreateRequest constructs a cmdman.CreateRequest for one replica of a
// Command. instanceName is the replica's concrete cmdman command name and
// scaleIndex its 1-based index; configHash is the command's computed hash.
// Reserved compose labels are merged with user labels.
func buildCreateRequest(
	spec ComposeSpec,
	nc Command,
	configHash string,
	instanceName string,
	scaleIndex int,
) cmdman.CreateRequest {
	// Inject the replica's identity as environment variables via AppendEnv (not
	// nc.Env) so they stay out of the config hash: the index is a property of the
	// replica, not the command config, so identical replicas must not look like
	// drift. Host-env inheritance is governed explicitly by ImportHostEnv below.
	appendEnv := []string{
		ENV_CMDMAN_COMPOSE_SCALE_INDEX + "=" + strconv.Itoa(scaleIndex),
		ENV_CMDMAN_COMPOSE_SCALE + "=" + strconv.Itoa(max(nc.Scale, 1)),
	}
	importHostEnv := nc.ImportHostEnv
	return cmdman.CreateRequest{
		Name:            instanceName,
		Dir:             nc.Dir,
		Argv:            nc.Args,
		Env:             nc.Env,
		ImportHostEnv:   &importHostEnv,
		AppendEnv:       appendEnv,
		RestartPolicy:   nc.RestartPolicy,
		MaxRetries:      nc.MaxRetries,
		StopSignal:      nc.StopSignal,
		Tty:             nc.Tty,
		ScrollbackBytes: nc.ScrollbackBytes,
		LogDriver:       nc.LogDriver,
		LogOpts:         nc.LogOpts,
		AutoRemove:      false, // compose owns lifecycle
		Labels:          BuildLabels(spec, nc, configHash, scaleIndex),
	}
}

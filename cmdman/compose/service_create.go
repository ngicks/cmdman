package compose

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

// CreateOption configures a Create operation.
type CreateOption struct {
	// RemoveOrphan causes stopped orphan commands to be removed.
	// Running orphans are reported and skipped (resolved-decision 4: no force in v1).
	// Ignored when Targets targets a subset.
	RemoveOrphan bool
	// Targets optionally narrows the operation to specific compose commands or
	// replicas and their transitive after-dependencies, every replica of those.
	// Empty targets every command. A replica index must lie within the scale the
	// spec declares for its command.
	Targets []Target
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
//     is set. Skipped when opts.Targets targets a subset.
//  4. Removes the surplus replicas a scale-down left behind. Skipped when a
//     target selects specific replicas, since such a target concerns those
//     replicas alone.
//  5. Executes create/recreate/unchanged actions for the targeted replicas and
//     aggregates outcomes.
func (s *Service) Create(
	ctx context.Context,
	spec ComposeSpec,
	opts CreateOption,
) (*CreateResult, error) {
	targets, err := resolveTargets(opts.Targets, declaredReplicas(spec))
	if err != nil {
		return nil, err
	}
	res, _, err := s.create(ctx, spec, opts.RemoveOrphan, targets)
	return res, err
}

// create is [Service.Create] for already resolved targets. held maps the cmdman
// command name of every replica whose create or recreate failed to that
// failure. Such a replica is the old one, a new one whose create_post failed,
// or gone, and no start should take it for the replica the spec describes.
func (s *Service) create(
	ctx context.Context,
	spec ComposeSpec,
	removeOrphan bool,
	targets targetSet,
) (_ *CreateResult, held map[string]error, _ error) {
	existing, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels: map[string]string{
			LabelWorkdir: spec.WorkDir,
			LabelProject: spec.Project,
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list existing commands: %w", err)
	}

	plan, err := ComputePlan(spec, existing)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"compute plan for project %q in %q: %w",
			spec.Project,
			spec.WorkDir,
			err,
		)
	}

	closure := resolveTargetCommands(spec, targets.names())

	var actions []ActionOutcome
	// Orphan handling is a whole-project concern; skip it when a subset is targeted.
	if len(targets) == 0 {
		orphanOutcomes := s.handleOrphans(ctx, spec, plan.Orphans, removeOrphan)
		actions = append(actions, orphanOutcomes...)
	}

	// Surplus replicas from a scale-down are reconciled away (scoped to the
	// targeted commands), regardless of --remove-orphan. A replica-scoped target
	// asks about its replicas only, so it leaves the surplus alone.
	if !targets.replicaScoped() {
		actions = append(
			actions, s.handleExcessReplicas(ctx, spec, plan.ExcessReplicas, closure)...)
	}

	for _, action := range plan.Actions {
		if _, ok := closure[action.Desired.Name]; !ok {
			continue
		}
		if !targets.covers(action.Desired.Name, action.ScaleIndex) {
			continue
		}
		outcome, err := s.executeAction(ctx, spec, action)
		if err != nil {
			// internal/unexpected error; individual cmd errors are in Err field
			return nil, nil, err
		}
		if outcome.Err != nil {
			if held == nil {
				held = make(map[string]error)
			}
			held[action.InstanceName] = outcome.Err
		}
		actions = append(actions, outcome)
	}

	// Every entry path that brings a project into existence lands here (Up calls
	// Create unconditionally), so this one site owns history-row creation.
	s.recordProject(ctx, spec)

	return &CreateResult{Actions: actions}, held, nil
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
		err := s.createWithHooks(
			ctx, s.specHookReplica(spec, nc, action.ScaleIndex), nc.Hooks, req)
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
		// The replica going away runs the hooks stored on it. The spec may
		// declare other hooks by now.
		old, oldHooks, err := storedHookReplica(*existing)
		if err != nil {
			werr := fmt.Errorf("recreate command %q: %w", disp, err)
			s.report(disp, PhaseError, werr, nil)
			return ActionOutcome{Command: disp, Action: "recreate", Err: werr}, nil
		}

		// A running/starting command is stopped before it can be removed and
		// recreated. The stop is surfaced as its own stopping → stopped step in the
		// trace so the user sees the command go down before it comes back. A stop
		// failure aborts the recreate (the still-running command must not be
		// removed out from under its live monitor). Any other command is removed
		// without a stop, and the stop releases stored for it run once it is gone.
		live := existing.State == model.EventTypeRunning ||
			existing.State == model.EventTypeStarting
		if live {
			contextkey.ValueSlogLoggerDefault(ctx).Info(
				"compose: stopping changed command before recreate",
				"project", spec.Project,
				"command", disp,
				"id", existing.ID,
				"state", existing.State,
			)
			s.report(disp, PhaseStopping, nil, nil)
			if _, err := s.stopWithHooks(ctx, old, oldHooks, existing.ID, nil); err != nil {
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
		err = s.removeReplica(ctx, old, oldHooks, cmdman.RemoveRequest{
			Targets: []string{existing.ID},
		}, live)
		if err != nil {
			werr := fmt.Errorf("remove command %q for recreate: %w", disp, err)
			s.report(disp, PhaseError, werr, nil)
			return ActionOutcome{Command: disp, Action: "recreate", Err: werr}, nil
		}

		req := buildCreateRequest(spec, nc, action.DesiredHash, instName, action.ScaleIndex)
		err = s.createWithHooks(
			ctx, s.specHookReplica(spec, nc, action.ScaleIndex), nc.Hooks, req)
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
	idx, scale := ScaleOf(e.ConfigJSON.Labels)
	if scale <= 1 || idx <= 0 {
		return name
	}
	return fmt.Sprintf("%s-%d", name, idx)
}

// replicaNamer returns the label of each stored replica of a project for
// outcome lines. It labels a replica the way start and stop progress does, by
// [instanceDisplayName], against the larger of the replica count spec declares
// and the highest scale index stored in entries. entries must be the whole
// project: the label of a replica depends on its siblings, targeted or not.
//
// The scale label of a stored replica is not consulted: a scale-up leaves it on
// the replicas it does not create, so the first replica would read as unscaled.
// The stored index keeps every label distinct when spec declares fewer replicas
// than are stored.
func replicaNamer(spec *ComposeSpec, entries []cmdmanEntry) func(cmdmanEntry) string {
	scales := storedReplicas(nil, entries)
	if spec != nil {
		for name, n := range declaredReplicas(*spec) {
			scales[name] = max(scales[name], n)
		}
	}
	return func(e cmdmanEntry) string {
		name := commandNameOf(e)
		return instanceDisplayName(Command{Name: name, Scale: scales[name]}, scaleIndexOf(e))
	}
}

// handleExcessReplicas stops (when live) and removes surplus replicas left by a
// scale-down, each inside the stop and remove hooks stored on it. A replica
// that is not live is removed without a stop, and the stop releases stored for
// it run once it is gone ([Service.removeReplica]). Only replicas whose command
// is in the target set are touched, so a subset operation never tears down a
// replica it was not asked about. Each removal is reported as its own removing
// → removed/error step.
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

		r, hooks, err := storedHookReplica(e)
		if err != nil {
			werr := fmt.Errorf("remove excess replica %q (%s): %w", disp, e.ID, err)
			s.report(disp, PhaseError, werr, nil)
			outcomes = append(outcomes, ActionOutcome{
				Command: disp, Action: "remove-excess", Err: werr,
			})
			continue
		}

		// Stop a live replica before removal so its monitor is not yanked.
		live := e.State == model.EventTypeRunning || e.State == model.EventTypeStarting
		if live {
			if _, err := s.stopWithHooks(ctx, r, hooks, e.ID, nil); err != nil {
				werr := fmt.Errorf("stop excess replica %q (%s): %w", disp, e.ID, err)
				s.report(disp, PhaseError, werr, nil)
				outcomes = append(outcomes, ActionOutcome{
					Command: disp, Action: "remove-excess", Err: werr,
				})
				continue
			}
		}

		err = s.removeReplica(ctx, r, hooks, cmdman.RemoveRequest{
			Targets: []string{e.ID},
			Force:   true,
		}, live)
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
// configured stop signal, and sends SIGKILL once timeout passes, nil for the
// command's stored stop timeout. The first error — from the call itself or any
// per-target result — is returned so the caller can abort the recreate.
func (s *Service) stopForRecreate(ctx context.Context, id string, timeout *time.Duration) error {
	results, err := s.svc.Stop(ctx, cmdman.StopRequest{Targets: []string{id}, Timeout: timeout})
	if err != nil {
		return err
	}
	return firstStopErr(results)
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
	// drift. The project identity is already part of the generated name, so
	// hashing it again would add nothing. Host-env inheritance is governed
	// explicitly by ImportHostEnv below.
	appendEnv := composeContextEnv(spec.Project, spec.WorkDir, nc.Name, scaleIndex, nc.Scale)
	importHostEnv := nc.ImportHostEnv
	injectEnv := nc.InjectEnv
	return cmdman.CreateRequest{
		Name:            instanceName,
		Dir:             nc.Dir,
		Argv:            nc.Args,
		Env:             nc.Env,
		ImportHostEnv:   &importHostEnv,
		InjectEnv:       &injectEnv,
		AppendEnv:       appendEnv,
		RestartPolicy:   nc.RestartPolicy,
		MaxRetries:      nc.MaxRetries,
		StopSignal:      nc.StopSignal,
		StopTimeout:     nc.StopGracePeriod,
		StopCommand:     nc.Stop,
		Tty:             nc.Tty,
		ScrollbackBytes: nc.ScrollbackBytes,
		LogDriver:       nc.LogDriver,
		LogOpts:         nc.LogOpts,
		AutoRemove:      false, // compose owns lifecycle
		Labels:          BuildLabels(spec, nc, configHash, scaleIndex),
	}
}

// composeContextEnv returns the environment entries that tell a process which
// replica of which compose project it runs for.
func composeContextEnv(project, workDir, command string, scaleIndex, scale int) []string {
	return []string{
		ENV_CMDMAN_COMPOSE_SCALE_INDEX + "=" + strconv.Itoa(scaleIndex),
		ENV_CMDMAN_COMPOSE_SCALE + "=" + strconv.Itoa(max(scale, 1)),
		ENV_CMDMAN_COMPOSE_WORK_DIR + "=" + workDir,
		ENV_CMDMAN_COMPOSE_WORK_DIR_HASH + "=" + workdirHash(workDir),
		ENV_CMDMAN_COMPOSE_PROJECT + "=" + project,
		ENV_CMDMAN_COMPOSE_COMMAND + "=" + command,
	}
}

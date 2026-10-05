package compose

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
)

// ProjectSummary describes one compose project discovered from stored command
// labels.
//
// CLI-output type: rendered as JSON (`--format json`) and through `--format` Go
// templates, where fields are referenced by Go name (.WorkDir, .ComposeFile).
// It carries no json field-name tags so `{{json .}}` and `{{.Field}}` agree.
type ProjectSummary struct {
	Project     string
	WorkDir     string
	ComposeFile string `json:",omitzero"`
	// Commands, Running, Exited and Failed count the project's replicas.
	Commands int
	Running  int
	Exited   int
	Failed   int
	// Intermediates counts the commands lifecycle hooks created for the
	// project's replicas: hook runs and resource holders.
	Intermediates int
}

// PsOption configures a Ps operation.
type PsOption struct {
	// Targets optionally narrows the listing to specific compose commands or
	// replicas. Empty lists the whole project. A replica index must name a
	// stored replica.
	Targets []Target
}

// CommandStatus describes one stored command in a compose project, together
// with the runtime state its monitor holds for the current run (the same
// fields [CommandRuntimeState] carries; a command with no live monitor keeps
// them zero).
// CLI-output type; see ProjectSummary for why it carries no json name tags.
type CommandStatus struct {
	// Command is the compose command of the replica, or of the replica an
	// intermediate belongs to. It is empty for a hook run whose replica is
	// gone.
	Command string
	ID      string
	Name    string
	// Intermediate is [IntermediateExec] or [IntermediateHolder] for a command
	// lifecycle hooks created, and empty for a replica.
	Intermediate string `json:",omitzero"`
	// Owner is the cmdman command name of the replica an intermediate belongs
	// to.
	Owner      string `json:",omitzero"`
	State      model.EventType
	ExitCode   *int     `json:",omitzero"`
	Argv       []string `json:",omitzero"`
	Title      string   `json:",omitzero"`
	Status     string   `json:",omitzero"`
	Detail     string   `json:",omitzero"`
	BellUnread bool
}

// ListProjects returns every compose project known to the cmdman store. A
// project whose replicas are all gone is still listed while intermediates of
// it remain.
func (s *Service) ListProjects(ctx context.Context) ([]ProjectSummary, error) {
	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels: map[string]string{
			LabelVersion: LabelVersionValue,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list compose commands: %w", err)
	}

	byProject := map[string]*ProjectSummary{}
	var keys []string
	summaryOf := func(project, workDir string) *ProjectSummary {
		key := workDir + "\x00" + project
		summary := byProject[key]
		if summary == nil {
			summary = &ProjectSummary{Project: project, WorkDir: workDir}
			byProject[key] = summary
			keys = append(keys, key)
		}
		return summary
	}

	for _, entry := range entries {
		if entry.ConfigJSON == nil {
			continue
		}
		labels := entry.ConfigJSON.Labels
		project := labels[LabelProject]
		workDir := labels[LabelWorkdir]
		if project == "" || workDir == "" {
			continue
		}
		summary := summaryOf(project, workDir)
		if summary.ComposeFile == "" {
			summary.ComposeFile = labels[LabelFile]
		}
		summary.Commands++
		switch entry.State {
		case model.EventTypeRunning:
			summary.Running++
		case model.EventTypeExited:
			summary.Exited++
		case model.EventTypeFailed:
			summary.Failed++
		}
	}

	// The store matches label values exactly, so each kind takes a query of its
	// own.
	for _, kind := range []string{IntermediateExec, IntermediateHolder} {
		intermediates, err := s.svc.List(ctx, cmdman.ListRequest{
			AllStates: true,
			Labels:    map[string]string{LabelIntermediate: kind},
		})
		if err != nil {
			return nil, fmt.Errorf("list compose hook commands: %w", err)
		}
		for _, entry := range intermediates {
			if entry.ConfigJSON == nil {
				continue
			}
			labels := entry.ConfigJSON.Labels
			project := labels[LabelHooksProject]
			workDir := labels[LabelHooksWorkdir]
			if project == "" || workDir == "" {
				continue
			}
			summaryOf(project, workDir).Intermediates++
		}
	}

	slices.SortFunc(keys, func(a, b string) int {
		pa, pb := byProject[a], byProject[b]
		if pa.Project < pb.Project {
			return -1
		}
		if pa.Project > pb.Project {
			return 1
		}
		if pa.WorkDir < pb.WorkDir {
			return -1
		}
		if pa.WorkDir > pb.WorkDir {
			return 1
		}
		return 0
	})

	summaries := make([]ProjectSummary, 0, len(keys))
	for _, key := range keys {
		summaries = append(summaries, *byProject[key])
	}
	return summaries, nil
}

// Ps lists commands in the selected compose project, filling each one's
// runtime columns from its monitor. The dial is the same bounded, failure-is-an-
// empty-column fan-out `ls` uses (see [cmdman.Service.RuntimeStates]), so a
// project whose monitors are gone still lists.
//
// The intermediates of the project are listed after the replicas of the
// command they belong to, and a hook run whose replica is gone after every
// command. Targets select an intermediate by the replica it belongs to, so
// such a hook run is listed only without targets.
func (s *Service) Ps(
	ctx context.Context,
	selection ProjectSelection,
	opts PsOption,
) ([]CommandStatus, error) {
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

	hookEntries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    hooksProjectLabels(selection.WorkDir, selection.Project),
	})
	if err != nil {
		return nil, fmt.Errorf("list project hook commands: %w", err)
	}
	var intermediates []ownedIntermediate
	for _, in := range ownIntermediates(hookEntries, entries) {
		if targets.coversIntermediate(in) {
			intermediates = append(intermediates, in)
		}
	}
	entries = targets.filter(entries)

	listed := slices.Clone(entries)
	for _, in := range intermediates {
		listed = append(listed, in.entry)
	}
	runtime := s.runtimeStates(ctx, listed)

	statuses := make([]CommandStatus, 0, len(listed))
	for _, entry := range entries {
		if entry.ConfigJSON == nil {
			continue
		}
		statuses = append(statuses, commandStatus(
			entry, entry.ConfigJSON.Labels[LabelCommand], runtime[entry.ID]))
	}
	for _, in := range intermediates {
		status := commandStatus(in.entry, in.command, runtime[in.entry.ID])
		status.Intermediate = in.kind
		status.Owner = in.owner
		statuses = append(statuses, status)
	}
	slices.SortFunc(statuses, func(a, b CommandStatus) int {
		switch {
		case a.Command == b.Command:
		case a.Command == "":
			return 1
		case b.Command == "":
			return -1
		default:
			return cmp.Compare(a.Command, b.Command)
		}
		switch aIn, bIn := a.Intermediate != "", b.Intermediate != ""; {
		case aIn && !bIn:
			return 1
		case !aIn && bIn:
			return -1
		case aIn:
			return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
		default:
			return cmp.Compare(a.ID, b.ID)
		}
	})
	return statuses, nil
}

func commandStatus(entry cmdmanEntry, command string, rs cmdman.RuntimeState) CommandStatus {
	return CommandStatus{
		Command:    command,
		ID:         entry.ID,
		Name:       entry.Name,
		State:      entry.State,
		ExitCode:   entry.ExitCode,
		Argv:       slices.Clone(entry.ConfigJSON.Argv),
		Title:      rs.Title,
		Status:     rs.Status,
		Detail:     rs.Detail,
		BellUnread: rs.BellUnread,
	}
}

// ownedIntermediate is an intermediate together with the replica it belongs
// to. command is empty, and scaleIndex 0, when that replica is not known.
type ownedIntermediate struct {
	entry      cmdmanEntry
	kind       string
	owner      string
	command    string
	scaleIndex int
}

// ownIntermediates returns the intermediates among entries, each with the
// replica it belongs to. A holder records its replica in its labels; a hook
// run is matched by name with its owner among replicas.
func ownIntermediates(entries, replicas []cmdmanEntry) []ownedIntermediate {
	byName := make(map[string]cmdmanEntry, len(replicas))
	for _, r := range replicas {
		byName[r.Name] = r
	}
	var out []ownedIntermediate
	for _, e := range entries {
		if e.ConfigJSON == nil {
			continue
		}
		labels := e.ConfigJSON.Labels
		kind := labels[LabelIntermediate]
		if kind == "" {
			continue
		}
		in := ownedIntermediate{
			entry:      e,
			kind:       kind,
			owner:      labels[LabelOwner],
			command:    labels[LabelResourceCommand],
			scaleIndex: scaleLabelValue(labels[LabelResourceScaleIndex]),
		}
		if owner, ok := byName[in.owner]; ok && in.command == "" {
			in.command, in.scaleIndex = commandNameOf(owner), scaleIndexOf(owner)
		}
		out = append(out, in)
	}
	return out
}

// coversIntermediate reports whether ts selects the replica in belongs to.
// An empty ts selects every intermediate.
func (ts targetSet) coversIntermediate(in ownedIntermediate) bool {
	if len(ts) == 0 {
		return true
	}
	_, ok := ts[in.command]
	return ok && ts.covers(in.command, in.scaleIndex)
}

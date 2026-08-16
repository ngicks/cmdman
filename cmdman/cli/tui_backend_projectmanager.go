package cli

import (
	"context"
	"slices"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/cmdman/tui"
)

// ProjectManager loads the whole project-manager view for one project. The
// project is resolved exactly as the Layout tab resolves it (identity first,
// then the cwd-active mux project, then the caller's selection), so a widget
// summoned with only a --mux-token still names the project it landed on — and
// a project without a mux: section is out of reach here for the same reason it
// is on the Layout tab: two of the three actions are mux actions.
func (b *serviceBackend) ProjectManager(
	ctx context.Context, projectName, composeFile string,
) (tui.ProjectManagerInfo, error) {
	selection, err := b.resolveLayoutSelection(ctx, projectName, composeFile)
	if err != nil {
		return tui.ProjectManagerInfo{}, err
	}

	// The counter is the D11 replica source: it counts the project's stored
	// commands per compose command, all states included. A failure here is the
	// store failing, which would leave every row's +/- working from a fabricated
	// zero, so it is returned rather than degraded.
	_, counter, err := b.compose.MuxLeafResolver(ctx, selection)
	if err != nil {
		return tui.ProjectManagerInfo{}, err
	}
	counts := make(map[string]int, len(selection.Spec.Commands))
	for _, c := range selection.Spec.Commands {
		// A command with no instances yet is not an error here: never created
		// reads as zero replicas, which is what the row should say.
		if n, countErr := counter(ctx, c.Name); countErr == nil {
			counts[c.Name] = n
		}
	}

	// Shown is best-effort for the same reason the layout marker is: no server
	// and no dashboard both mean "nothing is displaying a replica", which the
	// absent entry already says.
	shown, _ := b.compose.MuxScaleState(ctx, compose.MuxScaleStateOption{Selection: selection})

	return tui.ProjectManagerInfo{
		Project:  selection.Project,
		Path:     selection.Spec.ComposeFile,
		Services: serviceScaleInfos(selection.Spec, counts, shown),
		Layouts:  layoutsOf(ctx, selection),
	}, nil
}

// serviceScaleInfos builds the service rows: every compose command in
// definition order, its replica count, the replica its panes show, and whether
// the mux section makes it a cycle target (an unpinned leaf in some layout).
func serviceScaleInfos(
	spec *compose.ComposeSpec,
	counts, shown map[string]int,
) []tui.ServiceScaleInfo {
	targets := mux.CollectCycleTargets(*spec.Mux)
	infos := make([]tui.ServiceScaleInfo, len(spec.Commands))
	for i, c := range spec.Commands {
		infos[i] = tui.ServiceScaleInfo{
			Name:     c.Name,
			Replicas: counts[c.Name],
			Shown:    shown[c.Name],
			Cyclable: slices.Contains(targets, c.Name),
		}
	}
	return infos
}

// SetScale sets one service's replica count through the same compose path
// `cmdman compose scale` runs: an ephemeral override reconciled by an Up scoped
// to that service. The per-replica outcomes are aggregated the way the CLI
// aggregates them, so a scale that only partly landed is reported as a failure
// rather than as done.
func (b *serviceBackend) SetScale(
	ctx context.Context, projectName, composeFile, service string, replicas int,
) error {
	opts := compose.ScaleOption{
		File:   composeFile,
		Scales: map[string]int{service: replicas},
	}
	if composeFile == "" {
		opts.File = projectName
	}
	result, err := b.compose.Scale(ctx, opts)
	if err != nil {
		return err
	}
	return UpResultErr(result)
}

// CycleScale changes which replica a command's dashboard pane shows, wrapping
// compose.Service.MuxCycleScale: set > 0 selects that 1-based replica, set == 0
// advances by one. No session is named, so every dashboard window of the
// project moves together — a session-narrowed cycle is what makes the windows
// disagree and their shown replica unknowable (D14).
func (b *serviceBackend) CycleScale(
	ctx context.Context, projectName, composeFile, command string, set int,
) error {
	selection, err := compose.ResolveMuxSelectionByName(projectName, composeFile)
	if err != nil {
		return err
	}
	_, err = b.compose.MuxCycleScale(ctx, compose.MuxCycleScaleOption{
		Selection: selection,
		Command:   command,
		Position:  set,
	})
	return err
}

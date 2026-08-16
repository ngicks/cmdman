package cli

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/cmdman/tui"
)

// ProjectManager loads the whole project-manager view for one project. A
// project without a mux: section is out of reach here for the same reason it is
// on the Layout tab: two of the three actions are mux actions.
func (b *serviceBackend) ProjectManager(
	ctx context.Context, projectName, composeFile string,
) (tui.ProjectManagerInfo, error) {
	selection, err := b.resolveManagerSelection(ctx, projectName, composeFile)
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

// resolveManagerSelection resolves the project the panel manages: the compose
// target the invocation named (--file/--project-name), and only failing that
// the Layout tab's chain — identity first, then the cwd-active mux project,
// then the caller's own selection.
//
// The explicit target has to outrank detection (D17). A popup always opens
// inside some window, so the ambient identity probe always answers there, and
// a panel summoned for the row under the switcher's cursor would manage the
// project of the window it opened over instead. An explicit target that does
// not resolve is an error rather than a reason to detect something else: the
// caller named a project, so another one is not an improvement.
//
// --workdir travels with the target (D20): a project is (work directory,
// name), so a load that names only the file resolves it against the panel's own
// directory — a popup opens wherever it was summoned from, which is where every
// replica count and the layout marker's identity would then be looked up.
func (b *serviceBackend) resolveManagerSelection(
	ctx context.Context, projectName, composeFile string,
) (compose.ProjectSelection, error) {
	if b.file != "" || b.projectName != "" {
		return compose.ResolveMuxSelectionByName(b.projectName, b.file, b.workDir)
	}
	return b.resolveLayoutSelection(ctx, projectName, composeFile)
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
		File:    composeFile,
		WorkDir: b.workDir,
		Scales:  map[string]int{service: replicas},
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
	selection, err := compose.ResolveMuxSelectionByName(projectName, composeFile, b.workDir)
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

// SummonProjectManager opens the project-manager widget over the given project
// in a multiplexer floating pane — the switcher's m (D7). It goes through the
// one popup seam `cmdman tui --popup` uses (D1/D5), so driver autodetection
// lives in a single place and a driver that grows a popup implementation lights
// this up with it.
//
// The project travels as --file/--project-name plus the row's own work
// directory rather than as a token: the panel manages the row the cursor was
// on, not the window the popup opens over (D9/D17), and a compose file names a
// project only together with the directory it stands in (D20). The store and
// config targets are this process's own, because the child is started by the
// multiplexer server and inherits that server's environment rather than the
// TUI's.
//
// The popup is the whole UI for as long as it is up, so this returns when it
// closes; what the widget itself did is reported inside it, and only "there was
// no popup to open" comes back here (D4).
func (b *serviceBackend) SummonProjectManager(
	ctx context.Context, projectName, composeFile, workDir string,
) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	cfg := b.svc.Config()
	return RunTUIPopup(ctx, PopupConfig{
		Child:      projectManagerChildArgs(workDir, projectName, composeFile),
		Cwd:        b.cwd,
		Executable: exe,
		DataDir:    cfg.DataDir,
		RuntimeDir: cfg.RuntimeDir,
		ConfPath:   cfg.ConfigPath,
		// The TUI that summoned this owns the terminal underneath the popup.
		Silent: true,
	})
}

// projectManagerChildArgs is the summoned widget's argv. --workdir carries the
// summoned row's own work directory, which is the half of the project's identity
// the compose file cannot supply (D20): without it the child loads the named
// file against the directory the popup opened in, and manages a project of that
// directory's name instead. Any of the three may be empty — a never-run named
// def has no file path, and a group the project listing never claimed has only
// its name.
func projectManagerChildArgs(workDir, projectName, composeFile string) PopupChild {
	args := []string{"tui", "widget", "project-manager"}
	if workDir != "" {
		args = append(args, "--workdir", workDir)
	}
	if composeFile != "" {
		args = append(args, "--file", composeFile)
	}
	if projectName != "" {
		args = append(args, "--project-name", projectName)
	}
	return PopupChild{Args: args}
}

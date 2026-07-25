package cli

import (
	"context"
	"io"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
	"github.com/ngicks/cmdman/pkg/cmdman/tui"
)

// CycleMux cycles the mux layout for a compose project via compose.Service.MuxUp
// (the same path the CLI uses). An empty layout cycles to the next layout. mux
// owns its layout state through a persisted tmux window marker; the TUI keeps
// none. Stdout is discarded so mux's attach hint never bleeds into the TUI
// surface (the TUI runs inside tmux, so mux prints nothing anyway); no
// SessionName is passed, so the current tmux session is targeted.
func (b *serviceBackend) CycleMux(ctx context.Context, projectName, composeFile string) error {
	selection, err := compose.ResolveMuxSelectionByName(projectName, composeFile)
	if err != nil {
		return err
	}
	return b.compose.MuxUp(ctx, compose.MuxUpOption{
		Selection: selection,
		Stdout:    io.Discard,
	})
}

// resolveLayoutSelection resolves the "current" mux project for the Layout tab
// (D5): the cwd-active mux project, falling back to the Compose-tab selection
// identified by projectName/composeFile. The resolved project must declare a
// mux: section.
func resolveLayoutSelection(
	projectName, composeFile, workDir string,
) (compose.ProjectSelection, error) {
	// Prefer the cwd-active mux project. SelectMuxProject errors when no (or an
	// ambiguous set of) mux compose is associated with the cwd; in that case fall
	// back to the explicit Compose-tab selection.
	if sel, err := compose.SelectMuxProject(compose.NormalizeOpts{WorkDir: workDir}); err == nil {
		return sel, nil
	}
	return compose.ResolveMuxSelectionByName(projectName, composeFile)
}

// ListLayouts returns the current project's mux layouts in definition order plus
// the running dashboard's current layout marker. The project is resolved per D5
// (cwd-active mux project, falling back to the Compose-tab selection).
func (b *serviceBackend) ListLayouts(
	ctx context.Context, projectName, composeFile string,
) (tui.LayoutsInfo, error) {
	selection, err := resolveLayoutSelection(projectName, composeFile, b.workDir)
	if err != nil {
		return tui.LayoutsInfo{}, err
	}
	spec := *selection.Spec.Mux

	names := make([]string, len(spec.Layouts))
	for i, l := range spec.Layouts {
		names[i] = l.Name
	}
	info := tui.LayoutsInfo{
		Project: selection.Project,
		Path:    selection.Spec.ComposeFile,
		Names:   names,
		Current: -1,
	}

	// Read the running dashboard's current layout marker, best-effort: a missing
	// tmux server or no dashboard yields no rows (Current stays -1). A genuine
	// listing failure is not fatal here — the layouts list itself is still valid,
	// and -1 already encodes "current layout unknown".
	windows, listErr := mux.List(ctx, mux.ListOptions{
		Driver:   spec.Driver,
		Identity: selection.ProjectIdentity(),
	})
	if listErr == nil && len(windows) > 0 {
		info.Current = windows[0].Marker
	}
	return info, nil
}

// ApplyLayout applies the named layout to the project's running dashboard,
// starting one at that layout when none is running (D6). It reuses CycleMux's
// MuxUp path with an explicit layout selector.
func (b *serviceBackend) ApplyLayout(
	ctx context.Context, projectName, composeFile, layoutName string,
) error {
	selection, err := compose.ResolveMuxSelectionByName(projectName, composeFile)
	if err != nil {
		return err
	}
	return b.compose.MuxUp(ctx, compose.MuxUpOption{
		Selection: selection,
		Layout:    layoutName,
		Stdout:    io.Discard,
	})
}

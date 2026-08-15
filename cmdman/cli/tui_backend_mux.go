package cli

import (
	"context"
	"errors"
	"io"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/cmdman/tui"
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

// SwitchToProject takes the client to the target's window — the docked
// switcher's enter/click (D6). The landing is [mux.Land]'s: the window carrying
// the target's identity is focused, and a project with none yet gets the bare
// shell window a landing synthesizes at its work directory (D9). What the
// switcher still does not do is bring the project up — compose up stays the
// launcher's `S` (V6).
//
// The identity travels with the target rather than being derived here: it is
// hashed from the work directory as compose spells it, while the target's
// WorkDir is symlink-resolved for cwd comparison (see projectIdentity) and would
// address a window that does not exist. That resolved directory is still where a
// created window's shell opens — same directory, other spelling.
func (b *serviceBackend) SwitchToProject(ctx context.Context, target tui.SwitchTarget) error {
	if target.Identity == "" {
		// An empty identity matches every stamped window on the server, so it is
		// not a narrower search but a wrong one.
		return errors.New("no project identity to switch to")
	}
	selection := compose.ProjectSelection{WorkDir: target.WorkDir, Project: target.Project}
	_, err := mux.Land(ctx, mux.LandOptions{
		WindowName: selection.MuxWindowName(),
		Identity:   target.Identity,
		WorkDir:    target.WorkDir,
	})
	return err
}

// HideFrame takes the frame down around the caller's current window — the docked
// switcher's collapse gesture (D16/V8). The def to hide is read off the window,
// so nothing identifies it here; a window with no frame up is a no-op.
func (b *serviceBackend) HideFrame(ctx context.Context) error {
	return mux.FrameHide(ctx, mux.FrameOptions{
		Config: b.svc.Config(),
		Svc:    NewFrameSvc(b.svc),
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

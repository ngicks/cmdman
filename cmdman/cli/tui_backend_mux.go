package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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
//
// An empty workDir is the Compose tab's: its project came from the directory
// this TUI works on, so the invocation's own override is the right one. The
// project-manager widget names the loaded project's instead, which is the
// directory its dashboard window is identified by (D20).
func (b *serviceBackend) CycleMux(
	ctx context.Context, projectName, composeFile, workDir string,
) error {
	selection, err := compose.ResolveMuxSelectionByName(
		projectName, composeFile, b.targetWorkDir(workDir),
	)
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
		// mux.Land falls back to the window name when the identity is empty, so an
		// empty one would land on whatever window carries the bare `cmdman-<project>`
		// stamp — a same-named project under another work directory, brought up
		// without an identity of its own, wears exactly that.
		return errors.New("no project identity to switch to")
	}
	selection := compose.ProjectSelection{Project: target.Project}
	_, err := mux.Land(ctx, mux.LandOptions{
		WindowName: selection.MuxWindowName(),
		Identity:   target.Identity,
		WorkDir:    target.WorkDir,
	})
	return err
}

// identityProbe is one active-project detection probe that did not answer: what
// was asked and why it came back empty. D4 (as amended by D10) wants the
// failure message to name every probe rather than only the last, so the user
// learns which questions were even asked.
type identityProbe struct {
	probe  string
	reason string
}

func (p identityProbe) String() string { return p.probe + ": " + p.reason }

// joinProbes renders the probe trail for a D4 failure message. The separator is
// the TUI's own " · " rather than a semicolon: the reasons are wrapped errors
// and several of them carry semicolons of their own.
func joinProbes(probes []identityProbe) string {
	parts := make([]string, len(probes))
	for i, p := range probes {
		parts[i] = p.String()
	}
	return strings.Join(parts, " · ")
}

// ActiveIdentity resolves the active project's mux ownership stamp from the
// explicit --mux-token the backend was constructed with (D10), then from the
// window the caller is sitting in (D13). ok=false hands the caller back to
// Cwd() matching, so nothing here is an error: a missing server, a driver
// without an implementation and a stale token all read as "no answer" instead
// of taking the whole view down.
func (b *serviceBackend) ActiveIdentity(ctx context.Context) (string, bool) {
	identity, _ := b.probeActiveIdentity(ctx)
	return identity, identity != ""
}

// probeActiveIdentity is ActiveIdentity plus the trail D4's message is built
// from: every probe that was tried and did not answer, in probe order.
func (b *serviceBackend) probeActiveIdentity(ctx context.Context) (string, []identityProbe) {
	var tried []identityProbe
	env := os.Environ()

	const windowProbe = "enclosing window"
	// CurrentWindowID is client-relative, not process-relative, and has no honest
	// "don't know" (NOTES Q1): outside a multiplexer it still answers ok=true
	// with some other client's window. Same guard as the frame verbs'
	// frameWindowID (D13). Asked before the listing because with no token either,
	// there is nothing to look up and the listing is a multiplexer round trip.
	inMux := envOf(env, "TMUX") != "" || envOf(env, "ZELLIJ") != ""
	if b.muxToken == "" && !inMux {
		return "", []identityProbe{{windowProbe, "not inside a multiplexer"}}
	}

	// One listing serves both probes: its rows carry WindowID and Identity
	// together, so matching a window id against them yields the project and the
	// token's staleness in a single call — no undeclared muxctl state key, and
	// no window accepted that carries no ownership stamp (D13).
	windows, listErr := mux.List(ctx, mux.ListOptions{Env: env})

	if b.muxToken != "" {
		probe := "mux token " + strconv.Quote(b.muxToken)
		identity, found := identityOfWindow(windows, b.muxToken)
		switch {
		case listErr != nil:
			tried = append(tried, identityProbe{probe, listErr.Error()})
		case !found:
			// Not "no such window": a missing server and a live-but-unstamped
			// window produce no matching row either (NOTES Q2).
			tried = append(tried, identityProbe{probe, "matches no cmdman-owned window"})
		case identity == "":
			tried = append(tried, identityProbe{probe, "that window holds no project"})
		default:
			return identity, tried
		}
	}

	if !inMux {
		return "", append(tried, identityProbe{windowProbe, "not inside a multiplexer"})
	}
	if listErr != nil {
		return "", append(tried, identityProbe{windowProbe, listErr.Error()})
	}
	windowID, ok, err := mux.CurrentWindowID(ctx, mux.CurrentWindowOptions{Env: env})
	switch {
	case err != nil:
		tried = append(tried, identityProbe{windowProbe, err.Error()})
	case !ok:
		tried = append(tried, identityProbe{windowProbe, "cannot tell which window you are in"})
	default:
		if identity, found := identityOfWindow(windows, windowID); found && identity != "" {
			return identity, tried
		}
		tried = append(tried, identityProbe{windowProbe, windowID + " holds no project"})
	}
	return "", tried
}

// identityOfWindow reports the ownership stamp of the listed window with this
// id. found says the id named a listed window at all, which is what tells a
// stale token apart from one naming a window that holds no project.
func identityOfWindow(windows []mux.OwnedWindow, windowID string) (identity string, found bool) {
	for _, w := range windows {
		if w.WindowID == windowID {
			return w.Identity, true
		}
	}
	return "", false
}

// selectionByIdentity resolves an ownership stamp back to the project that
// carries it, by matching the project listing's own precomputed Identity. The
// stamp is a hash and cannot be read backwards, and rebuilding one from a
// listed workdir would hash the symlink-resolved form into a window that does
// not exist (see projectIdentity).
//
// The matched row's directory travels into the load: the caller that owns this
// stamp is a popup or a bind-key invocation standing somewhere else entirely, so
// a load without it reads the file against that directory and finds none of the
// project's commands (D20).
func (b *serviceBackend) selectionByIdentity(
	ctx context.Context, identity string,
) (compose.ProjectSelection, error) {
	infos, err := b.ListProjects(ctx)
	if err != nil {
		return compose.ProjectSelection{}, err
	}
	for _, p := range infos {
		if p.Identity != "" && p.Identity == identity {
			return compose.ResolveMuxSelectionByName(p.Name, p.Path, p.Workdir)
		}
	}
	return compose.ProjectSelection{}, fmt.Errorf("no listed project carries identity %q", identity)
}

// resolveLayoutSelection resolves the "current" mux project for the Layout tab
// and the project-manager widget, identity first (D3/D5): the project whose mux
// window the caller is in — or whose token it was handed — then the cwd-active
// mux project, then the Compose-tab selection identified by
// projectName/composeFile. The resolved project must declare a mux: section.
func (b *serviceBackend) resolveLayoutSelection(
	ctx context.Context, projectName, composeFile string,
) (compose.ProjectSelection, error) {
	identity, tried := b.probeActiveIdentity(ctx)
	if identity != "" {
		sel, err := b.selectionByIdentity(ctx, identity)
		if err == nil {
			return sel, nil
		}
		tried = append(tried, identityProbe{
			"project identity " + strconv.Quote(identity), err.Error(),
		})
	}

	// The cwd-active mux project. SelectMuxProject errors when no (or an
	// ambiguous set of) mux compose is associated with the cwd; in that case fall
	// back to the explicit Compose-tab selection.
	sel, cwdErr := compose.SelectMuxProject(compose.NormalizeOpts{WorkDir: b.workDir})
	if cwdErr == nil {
		return sel, nil
	}
	sel, nameErr := compose.ResolveMuxSelectionByName(projectName, composeFile, b.workDir)
	if nameErr == nil {
		return sel, nil
	}

	tried = append(tried,
		identityProbe{"working directory " + strconv.Quote(b.cwd), cwdErr.Error()},
		identityProbe{"compose selection " + strconv.Quote(projectName), nameErr.Error()},
	)
	return compose.ProjectSelection{}, fmt.Errorf("no active project: %s", joinProbes(tried))
}

// ListLayouts returns the current project's mux layouts in definition order plus
// the running dashboard's current layout marker. The project is resolved per
// D3/D5 (the project whose mux window the caller is in, then the cwd-active mux
// project, then the Compose-tab selection).
func (b *serviceBackend) ListLayouts(
	ctx context.Context, projectName, composeFile string,
) (tui.LayoutsInfo, error) {
	selection, err := b.resolveLayoutSelection(ctx, projectName, composeFile)
	if err != nil {
		return tui.LayoutsInfo{}, err
	}
	return layoutsOf(ctx, selection), nil
}

// layoutsOf projects an already-resolved selection onto the Layout-tab data.
// It is where the layout listing lives so the project-manager load, which
// resolves its project once for the whole view, reads the same marker rather
// than resolving a second time.
func layoutsOf(ctx context.Context, selection compose.ProjectSelection) tui.LayoutsInfo {
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
	return info
}

// ApplyLayout applies the named layout to the project's running dashboard,
// starting one at that layout when none is running (D6). It reuses CycleMux's
// MuxUp path with an explicit layout selector, workDir included.
func (b *serviceBackend) ApplyLayout(
	ctx context.Context, projectName, composeFile, workDir, layoutName string,
) error {
	selection, err := compose.ResolveMuxSelectionByName(
		projectName, composeFile, b.targetWorkDir(workDir),
	)
	if err != nil {
		return err
	}
	return b.compose.MuxUp(ctx, compose.MuxUpOption{
		Selection: selection,
		Layout:    layoutName,
		Stdout:    io.Discard,
	})
}

package compose

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// WithFrameSvc gives the service the seam mux's frame verbs need, for the
// default_frame [Service.MuxUp] shows around the window it brings up (V9): a
// managed: true entry (D19/V7) has a supervised command to adopt, restart or
// create before a pane can view it.
//
// It is a dependency of its own rather than something taken off the cmdman
// service because no cmdman type is a [mux.FrameSvc] — the translation lives in
// the wiring layer (cmdman/cli.NewFrameSvc), which is also where this option is
// set. A Service built without it shows any def that has no managed entry, and
// warns on one that has, exactly as an auto-show that could not load a def does.
func WithFrameSvc(svc mux.FrameSvc) ServiceOption {
	return func(s *Service) { s.frameSvc = svc }
}

// MuxUpOption configures [Service.MuxUp].
type MuxUpOption struct {
	// Selection is the resolved compose project; it must declare a "mux:" section.
	Selection ProjectSelection
	// WindowName overrides the display name of a newly created window. Empty
	// preserves [ProjectSelection.MuxWindowName].
	WindowName string
	// Layout selects a layout by name or 0-based index. Empty cycles to the next,
	// unless KeepLayout says otherwise.
	Layout string
	// KeepLayout makes an empty Layout re-apply the layout the dashboard is
	// already on instead of advancing to the next (see [mux.RunOptions.KeepLayout]).
	// Callers bringing a project up set it: "make this project running" is not a
	// request to move its layout.
	KeepLayout bool
	// SessionName targets a specific multiplexer session. Empty lets the driver
	// resolve the current (or default "cmdman") session.
	SessionName string
	// KeepCurrentWindow forbids repurposing the caller's current window (see
	// [mux.RunOptions.KeepCurrentWindow]). The launcher sets it: a popup is
	// summoned over whatever window the user is on, which is not an invitation
	// to build a dashboard in it.
	KeepCurrentWindow bool
	// Stdout is where the attach hint is printed when running outside a
	// multiplexer. Empty defaults to os.Stdout (see [mux.RunOptions.Stdout]).
	Stdout io.Writer
}

func (o MuxUpOption) windowName() string {
	if o.WindowName != "" {
		return o.WindowName
	}
	return o.Selection.MuxWindowName()
}

// MuxUp builds the project's mux dashboard and applies a layout, reading any
// persisted per-command scale positions first. It resolves each leaf via
// [Service.MuxLeafResolver] so replicas cycle, and stamps the window with the
// project identity so [Service.MuxDown] and cycle-scale can find it.
//
// The configured default_frame is shown around the window it just brought up on
// the same terms standalone `mux up` has (V9): the seam [WithFrameSvc] set
// reaches [mux.Run] for what a managed entry needs, and a def that is missing or
// broken costs a warning, never the dashboard.
func (s *Service) MuxUp(ctx context.Context, opts MuxUpOption) error {
	selection := opts.Selection
	spec := *selection.Spec.Mux

	cfg := s.svc.Config()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cmdman binary: %w", err)
	}

	resolver, replicas, err := s.MuxLeafResolver(ctx, selection)
	if err != nil {
		return err
	}

	scalePositions, err := mux.ReadScaleState(ctx, mux.ScaleStateOptions{
		Driver:      spec.Driver,
		SessionName: opts.SessionName,
		Identity:    selection.ProjectIdentity(),
	})
	if err != nil {
		return fmt.Errorf("read scale state: %w", err)
	}

	built, err := mux.Build(ctx, mux.BuildOptions{
		Spec:     spec,
		Resolver: resolver,
		Replicas: replicas,
		Opts: mux.PaneArgvOpts{
			Executable: exe,
			DataDir:    cfg.DataDir,
			RuntimeDir: cfg.RuntimeDir,
		},
		ScalePositions: scalePositions,
	})
	if err != nil {
		return err
	}

	return mux.Run(ctx, built, mux.RunOptions{
		SessionName:       opts.SessionName,
		WindowName:        opts.windowName(),
		Identity:          selection.ProjectIdentity(),
		Layout:            opts.Layout,
		KeepLayout:        opts.KeepLayout,
		KeepCurrentWindow: opts.KeepCurrentWindow,
		Config:            cfg,
		Svc:               s.frameSvc,
		Stdout:            opts.Stdout,
	})
}

// MuxLandOption configures [Service.MuxLand].
type MuxLandOption struct {
	// Selection is the resolved compose project. Unlike the other mux options it
	// need not declare a "mux:" section: a project without one still gets a
	// window to land in (D9).
	Selection ProjectSelection
	// WindowName overrides the display name of a newly created window. Empty
	// preserves [ProjectSelection.MuxWindowName].
	WindowName string
	// SessionName targets a specific multiplexer session. Empty searches the
	// whole server and creates in the caller's current session.
	SessionName string
}

func (o MuxLandOption) windowName() string {
	if o.WindowName != "" {
		return o.WindowName
	}
	return o.Selection.MuxWindowName()
}

// MuxLand takes the caller to the project's window, creating a bare shell
// window at the project's work directory when the project has none — the
// mux-less project's landing (D9) and the "dashboard was torn down" case. It is
// the focus half of the launcher's `S` (D4/D10): the bring-up is [Service.MuxUp]
// (or, for a mux-less project, nothing at all).
//
// The driver comes from the project's mux section when it has one, so a
// dashboard built on a dedicated socket is found on that socket; a project
// without a section falls back to autodetection, which is the only thing it can
// mean. Like [Service.MuxDown] it never touches the underlying cmdman service,
// so it is safe on a Service built with NewService(nil).
func (s *Service) MuxLand(ctx context.Context, opts MuxLandOption) (mux.LandResult, error) {
	selection := opts.Selection

	var driver muxctl.DriverSpec
	if selection.Spec != nil && selection.Spec.Mux != nil {
		driver = selection.Spec.Mux.Driver
	}

	return mux.Land(ctx, mux.LandOptions{
		Driver:      driver,
		SessionName: opts.SessionName,
		WindowName:  opts.windowName(),
		Identity:    selection.ProjectIdentity(),
		WorkDir:     selection.WorkDir,
	})
}

// MuxDownOption configures [Service.MuxDown].
type MuxDownOption struct {
	// Selection is the resolved compose project; it must declare a "mux:" section.
	Selection ProjectSelection
	// SessionName narrows teardown to a single session. Empty is server-wide.
	SessionName string
	// KillCreated closes the windows cmdman created rather than emptying them,
	// leaving no shell pane and no frame behind (see
	// [mux.DownOptions.KillCreated]). A window cmdman borrowed from the caller
	// is restored whatever this says.
	KillCreated bool
	// Stdout is where the per-window lines are written. Empty defaults to
	// os.Stdout (see [mux.DownOptions.Stdout]).
	Stdout io.Writer
}

// MuxDown tears down the project's dashboard windows. It needs no leaf
// resolution — only the project identity and the spec's driver options — so it
// never touches the underlying cmdman service; it is a method for a uniform mux
// surface. Because it never dereferences s.svc, it is safe to invoke on a
// Service built with NewService(nil). The supervised commands keep running;
// only the disposable viewers are torn down.
func (s *Service) MuxDown(ctx context.Context, opts MuxDownOption) error {
	selection := opts.Selection
	spec := *selection.Spec.Mux
	return mux.Down(ctx, mux.DownOptions{
		Driver: spec.Driver,
		// SessionName is a narrowing filter only; it is not used to derive the
		// identity. An explicit session keeps the scan in one session.
		SessionName: opts.SessionName,
		Identity:    selection.ProjectIdentity(),
		KillCreated: opts.KillCreated,
		Stdout:      opts.Stdout,
	})
}

// MuxLsOption configures [Service.MuxLs].
type MuxLsOption struct {
	// Selection is the resolved compose project; it must declare a "mux:" section.
	Selection ProjectSelection
	// SessionName narrows the listing to a single session. Empty is server-wide.
	SessionName string
}

// MuxLsResult is the aggregate result of [Service.MuxLs]: the discovered
// dashboard windows plus the data the SCALE column needs (live replica counts
// and the spec's cycle-scale targets).
type MuxLsResult struct {
	// Windows are the discovered cmdman-owned dashboard windows.
	Windows []mux.OwnedWindow
	// ReplicaCounts maps each cycle target to its live replica count. Commands
	// whose count could not be resolved are absent (rendered as "pos/?").
	ReplicaCounts map[string]int
	// CycleTargets are the spec's unpinned leaf command names (cycle-scale
	// targets), sorted and deduplicated.
	CycleTargets []string
}

// MuxLs discovers the project's dashboard windows and resolves the live replica
// counts for the SCALE column. Replica-count resolution is best-effort: a
// command whose count cannot be resolved is simply omitted from ReplicaCounts.
func (s *Service) MuxLs(ctx context.Context, opts MuxLsOption) (MuxLsResult, error) {
	selection := opts.Selection
	spec := *selection.Spec.Mux

	windows, err := mux.List(ctx, mux.ListOptions{
		Driver:      spec.Driver,
		SessionName: opts.SessionName,
		Identity:    selection.ProjectIdentity(),
	})
	if err != nil {
		return MuxLsResult{}, err
	}

	targets := mux.CollectCycleTargets(spec)

	// Resolve live replica counts for the SCALE column. Commands whose count
	// cannot be resolved (store unavailable, replica missing live) are left
	// absent from the map and render as "pos/?". When the Service was built with
	// no underlying cmdman service (NewService(nil)), skip resolution entirely so
	// window listing still succeeds and every count renders as "?".
	replicaCounts := make(map[string]int, len(targets))
	if s.svc != nil {
		if _, counter, counterErr := s.MuxLeafResolver(ctx, selection); counterErr == nil &&
			counter != nil {
			for _, t := range targets {
				if n, err := counter(ctx, t); err == nil {
					replicaCounts[t] = n
				}
			}
		}
	}

	return MuxLsResult{
		Windows:       windows,
		ReplicaCounts: replicaCounts,
		CycleTargets:  targets,
	}, nil
}

// MuxCycleScaleOption configures [Service.MuxCycleScale].
type MuxCycleScaleOption struct {
	// Selection is the resolved compose project; it must declare a "mux:" section.
	Selection ProjectSelection
	// SessionName narrows the operation to a single session. Empty is server-wide.
	SessionName string
	// Command is the compose service name to advance.
	Command string
	// Position is the target replica (1-based). 0 means "advance by one".
	Position int
	// Env is the invoker's process env, consulted for driver autodetection (see
	// [mux.CycleScaleOptions.Env]). It is carried rather than read here because
	// the process doing the cycling need not be the one the user invoked, and
	// only the invoker's $TMUX names the server they are looking at. Empty
	// defaults to os.Environ().
	Env []string
}

// MuxCycleScale advances the replica position for a command across all matching
// dashboard windows, returning the per-window results. A partial result plus a
// non-nil error is possible when some windows succeed and others fail (see
// [mux.CycleScale]).
func (s *Service) MuxCycleScale(
	ctx context.Context,
	opts MuxCycleScaleOption,
) (mux.CycleScaleResult, error) {
	selection := opts.Selection
	spec := *selection.Spec.Mux

	cfg := s.svc.Config()
	exe, err := os.Executable()
	if err != nil {
		return mux.CycleScaleResult{}, fmt.Errorf("locate cmdman binary: %w", err)
	}

	resolver, replicas, err := s.MuxLeafResolver(ctx, selection)
	if err != nil {
		return mux.CycleScaleResult{}, err
	}

	return mux.CycleScale(ctx, mux.CycleScaleOptions{
		Spec:     spec,
		Resolver: resolver,
		Replicas: replicas,
		Opts: mux.PaneArgvOpts{
			Executable: exe,
			DataDir:    cfg.DataDir,
			RuntimeDir: cfg.RuntimeDir,
		},
		Identity:    selection.ProjectIdentity(),
		SessionName: opts.SessionName,
		Command:     opts.Command,
		Position:    opts.Position,
		Env:         opts.Env,
	})
}

// MuxScaleStateOption configures [Service.MuxScaleState].
type MuxScaleStateOption struct {
	// Selection is the resolved compose project; it must declare a "mux:" section.
	Selection ProjectSelection
	// SessionName narrows the read to a single session. Empty is server-wide.
	SessionName string
}

// MuxScaleState reports which replica each of the project's commands is
// currently showing in its dashboard panes — the positions cycle-scale persists
// as [muxctl.StateKeyScale] window state. A command absent from the map has no
// answer; the caller renders it as unknown rather than as replica 1.
//
// A command is reported only when every window that holds a position for it
// holds the same one (D14). Agreement is not an invariant: a session-narrowed
// cycle-scale leaves the windows it did not visit behind, and the merged read
// [mux.ReadScaleState] performs is last-row-wins, which would answer for one
// session with the other session's position.
//
// A window holding no position for a command abstains instead of counting as
// replica 1: [Service.MuxUp] seeds a window it builds from the read below
// without writing the state back, so an unset window is most likely displaying
// the very position the others recorded — and one stray identity-stamped window
// would otherwise render the whole project unknown.
//
// Like [Service.MuxDown] and [Service.MuxLs] it never touches the underlying
// cmdman service, so it is safe to invoke on a Service built with
// NewService(nil).
func (s *Service) MuxScaleState(
	ctx context.Context,
	opts MuxScaleStateOption,
) (map[string]int, error) {
	selection := opts.Selection
	spec := *selection.Spec.Mux

	windows, err := mux.List(ctx, mux.ListOptions{
		Driver:      spec.Driver,
		SessionName: opts.SessionName,
		Identity:    selection.ProjectIdentity(),
	})
	if err != nil {
		return nil, err
	}
	return agreedScalePositions(windows), nil
}

// agreedScalePositions folds the per-window scale positions into the ones every
// window that holds an opinion agrees on. A command whose windows disagree is
// dropped and stays dropped — a third window matching the first must not
// resurrect it. Returns nil when nothing is agreed, mirroring
// [mux.ReadScaleState]'s empty answer.
func agreedScalePositions(windows []mux.OwnedWindow) map[string]int {
	agreed := make(map[string]int)
	diverged := make(map[string]struct{})
	for _, w := range windows {
		for command, pos := range w.ScalePositions {
			if _, bad := diverged[command]; bad {
				continue
			}
			switch prev, seen := agreed[command]; {
			case !seen:
				agreed[command] = pos
			case prev != pos:
				delete(agreed, command)
				diverged[command] = struct{}{}
			}
		}
	}
	if len(agreed) == 0 {
		return nil
	}
	return agreed
}

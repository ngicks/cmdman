package compose

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

// MuxUpOption configures [Service.MuxUp].
type MuxUpOption struct {
	// Selection is the resolved compose project; it must declare a "mux:" section.
	Selection ProjectSelection
	// Layout selects a layout by name or 0-based index. Empty cycles to the next.
	Layout string
	// SessionName targets a specific multiplexer session. Empty lets the driver
	// resolve the current (or default "cmdman") session.
	SessionName string
	// Stdout is where the attach hint is printed when running outside a
	// multiplexer. Empty defaults to os.Stdout (see [mux.RunOptions.Stdout]).
	Stdout io.Writer
}

// MuxUp builds the project's mux dashboard and applies a layout, reading any
// persisted per-command scale positions first. It resolves each leaf via
// [Service.MuxLeafResolver] so replicas cycle, and stamps the window with the
// project identity so [Service.MuxDown] and cycle-scale can find it.
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
		SessionName: opts.SessionName,
		WindowName:  selection.MuxWindowName(),
		Identity:    selection.ProjectIdentity(),
		Layout:      opts.Layout,
		Stdout:      opts.Stdout,
	})
}

// MuxDownOption configures [Service.MuxDown].
type MuxDownOption struct {
	// Selection is the resolved compose project; it must declare a "mux:" section.
	Selection ProjectSelection
	// SessionName narrows teardown to a single session. Empty is server-wide.
	SessionName string
	// Stdout is where per-restored-window lines are written. Empty defaults to
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
		// WindowName feeds identity derivation only when Identity is empty
		// (unnamed project): it keeps down aligned with what MuxUp stamped.
		WindowName: selection.MuxWindowName(),
		Stdout:     opts.Stdout,
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

	// For an unnamed project (identity ""), fall back to the window name
	// ("cmdman") so the filter still matches what up stamped.
	identity := selection.ProjectIdentity()
	if identity == "" {
		identity = selection.MuxWindowName()
	}

	windows, err := mux.List(ctx, mux.ListOptions{
		Driver:      spec.Driver,
		SessionName: opts.SessionName,
		Identity:    identity,
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
	})
}

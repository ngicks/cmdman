package mux

import (
	"context"
	"errors"
	"fmt"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// Resolver maps a cmdman-layer leaf (command name/ID or compose service name)
// plus a 1-based scale index to the resolved cmdman command ID. [Build] calls
// Resolver once per realized leaf.
//
// scaleIndex is 0 when the leaf is unpinned and no replica cycling applies (the
// standalone `cmdman mux` case, where the leaf names a concrete command);
// otherwise it is the replica to resolve.
//
// Implementations:
//   - cmdman mux: a thin wrapper over cmdman.Service.Inspect; a positive
//     scaleIndex resolves the suffixed name "<leaf>-<scaleIndex>".
//   - cmdman compose mux: a wrapper over compose.Service replica resolution.
type Resolver func(ctx context.Context, leafName string, scaleIndex int) (id string, err error)

// ReplicaCounter reports how many scale replicas a leaf's command has (>= 1).
// [Build] uses it only to resolve the current cycle position for leaves that
// pin no scale index. A nil ReplicaCounter disables cycling: every unpinned
// leaf resolves once at scale index 0 (the standalone `cmdman mux` behavior).
type ReplicaCounter func(ctx context.Context, leafName string) (count int, err error)

// PaneArgvOpts holds the per-pane argv parameters carried into every leaf's
// resolved command.
type PaneArgvOpts struct {
	// Executable is the path to the cmdman binary used for in-pane processes.
	// Required; typically os.Executable() at the caller.
	Executable string
	// DataDir / RuntimeDir flow into --data-dir / --runtime-dir so the pane
	// process talks to the same cmdman runtime as its parent.
	DataDir    string
	RuntimeDir string
}

// BuildOptions collects all parameters for [Build].
type BuildOptions struct {
	Spec     Spec
	Resolver Resolver
	Replicas ReplicaCounter
	Opts     PaneArgvOpts
	// ScalePositions holds 1-based per-command positions for cycling leaves.
	// A missing key defaults to position 1. If the stored position exceeds the
	// live replica count n, it wraps as ((pos-1) % n) + 1.
	ScalePositions map[string]int
}

// Build resolves each leaf's command via resolver and emits a [muxctl.MuxSpec]
// with concrete argv.
//
// Each spec layout maps to exactly one muxctl layout (no expansion). Unpinned
// cycling leaves (Scale == 0 with a non-nil ReplicaCounter whose count > 1)
// resolve the replica selected by ScalePositions[command] (defaulting to 1,
// wrapping if out of range), and get CycleKey set to the command name.
//
// Duplicate resolved commands within a single realized layout are rejected (one
// pane per command replica).
func Build(ctx context.Context, opts BuildOptions) (muxctl.MuxSpec, error) {
	if opts.Opts.Executable == "" {
		return muxctl.MuxSpec{}, errors.New("mux: PaneArgvOpts.Executable is required")
	}
	if opts.Resolver == nil {
		return muxctl.MuxSpec{}, errors.New("mux: Resolver is required")
	}

	out := muxctl.MuxSpec{
		Driver:    opts.Spec.Driver,
		DriverOpt: opts.Spec.DriverOpt,
		Layouts:   make([]muxctl.Layout, 0, len(opts.Spec.Layouts)),
	}
	for i, l := range opts.Spec.Layouts {
		layout, err := buildLayout(
			ctx,
			l,
			opts.Resolver,
			opts.Replicas,
			opts.Opts,
			opts.ScalePositions,
		)
		if err != nil {
			return muxctl.MuxSpec{}, fmt.Errorf("layouts[%d] (%q): %w", i, l.Name, err)
		}
		out.Layouts = append(out.Layouts, layout)
	}
	if err := out.Validate(); err != nil {
		return muxctl.MuxSpec{}, err
	}
	return out, nil
}

// buildLayout converts one cmdman-layer layout into exactly one muxctl layout.
// scalePositions maps command names to 1-based positions for cycling leaves.
func buildLayout(
	ctx context.Context,
	l Layout,
	resolver Resolver,
	replicas ReplicaCounter,
	opts PaneArgvOpts,
	scalePositions map[string]int,
) (muxctl.Layout, error) {
	seen := make(map[string]struct{})
	root, err := buildPane(ctx, l.Root, resolver, opts, seen, replicas, scalePositions)
	if err != nil {
		return muxctl.Layout{}, err
	}
	return muxctl.Layout{Name: l.Name, Root: root}, nil
}

func buildPane(
	ctx context.Context,
	p PaneSpec,
	resolver Resolver,
	opts PaneArgvOpts,
	seen map[string]struct{},
	replicas ReplicaCounter,
	scalePositions map[string]int,
) (muxctl.PaneSpec, error) {
	switch {
	case p.IsLeaf():
		var (
			idx      int
			cycleKey string
			name     string
		)

		if p.Scale > 0 {
			// Pinned leaf: resolve the explicit replica.
			idx = p.Scale
			name = fmt.Sprintf("%s-%d", p.Command, idx)
		} else if replicas != nil {
			// Unpinned with a ReplicaCounter: this is a cycle-scale target.
			// CycleKey is always set so @cmdman_leaf is stamped and the pane is
			// locatable by cycle-scale even when n == 1.
			n, err := replicas(ctx, p.Command)
			if err != nil {
				return muxctl.PaneSpec{}, fmt.Errorf("count replicas of %q: %w", p.Command, err)
			}
			n = max(n, 1)
			storedPos := 1
			if scalePositions != nil {
				if sp, ok := scalePositions[p.Command]; ok {
					storedPos = sp
				}
			}
			idx = ((storedPos - 1) % n) + 1
			cycleKey = p.Command
			name = fmt.Sprintf("%s-%d", p.Command, idx)
		} else {
			// Standalone mux (nil ReplicaCounter): resolve at index 0, no suffix.
			idx = 0
			name = p.Command
		}

		id, err := resolver(ctx, p.Command, idx)
		if err != nil {
			return muxctl.PaneSpec{}, fmt.Errorf("resolve leaf %q: %w", p.Command, err)
		}
		if _, dup := seen[id]; dup {
			return muxctl.PaneSpec{}, fmt.Errorf(
				"duplicate command %q (replica %d) in layout", p.Command, idx)
		}
		seen[id] = struct{}{}

		return muxctl.PaneSpec{
			Leaf: muxctl.Leaf{
				Name:     name,
				Cmd:      paneArgv(opts, p.Mode, id),
				CmdOpt:   p.CmdOpt,
				Focus:    p.Focus,
				CycleKey: cycleKey,
			},
		}, nil

	case p.IsContainer():
		children := make([]muxctl.PaneSpec, len(p.Panes))
		for i, child := range p.Panes {
			built, err := buildPane(ctx, child, resolver, opts, seen, replicas, scalePositions)
			if err != nil {
				return muxctl.PaneSpec{}, fmt.Errorf("panes[%d]: %w", i, err)
			}
			children[i] = built
		}
		return muxctl.PaneSpec{
			Container: muxctl.Container{
				Dir:    muxctl.Direction(p.Dir),
				Splits: p.Splits,
				Panes:  children,
			},
		}, nil

	default:
		return muxctl.PaneSpec{}, errors.New(
			"mux: pane must be a leaf (command:) or a container (dir+splits+panes)",
		)
	}
}

// paneArgv builds the in-pane argv for a leaf:
//
//   - ModeAttach / "" (default): cmdman [--data-dir D] [--runtime-dir R] attach <id>
//   - ModeLogs:                  cmdman [--data-dir D] [--runtime-dir R] logs --sticky <id>
//
// Sticky-attach is the default (no flag needed; opt out with `--auto-exit`),
// and `logs --sticky` mirrors that — the pane stays open across command
// restarts, with injected `#|`-prefixed meta lines marking each exit.
func paneArgv(opts PaneArgvOpts, mode Mode, id string) []string {
	argv := []string{opts.Executable}
	if opts.DataDir != "" {
		argv = append(argv, "--data-dir", opts.DataDir)
	}
	if opts.RuntimeDir != "" {
		argv = append(argv, "--runtime-dir", opts.RuntimeDir)
	}
	switch mode {
	case ModeLogs:
		argv = append(argv, "logs", "--sticky", id)
	default:
		argv = append(argv, "attach", id)
	}
	return argv
}

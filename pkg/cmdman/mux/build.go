package mux

import (
	"context"
	"errors"
	"fmt"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// Resolver maps a cmdman-layer leaf name (a cmdman command name/ID or a
// compose service name) to the resolved cmdman command ID. [Build] calls
// Resolver once per leaf.
//
// Implementations:
//   - cmdman mux: a thin wrapper over cmdman.Service.Inspect that returns
//     the resolved entry's ID.
//   - cmdman compose mux: a wrapper over
//     compose.Service.ResolveCommandID(selection, name).
type Resolver func(ctx context.Context, leafName string) (id string, err error)

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

// Build resolves each leaf's Command via resolver and emits a
// [muxctl.MuxSpec] with concrete argv. Duplicate cmdman commands within a
// single layout are rejected (one pane per command).
func Build(
	ctx context.Context,
	spec Spec,
	resolver Resolver,
	opts PaneArgvOpts,
) (muxctl.MuxSpec, error) {
	if opts.Executable == "" {
		return muxctl.MuxSpec{}, errors.New("mux: PaneArgvOpts.Executable is required")
	}
	if resolver == nil {
		return muxctl.MuxSpec{}, errors.New("mux: Resolver is required")
	}

	out := muxctl.MuxSpec{
		Driver:    spec.Driver,
		DriverOpt: spec.DriverOpt,
		Layouts:   make([]muxctl.Layout, 0, len(spec.Layouts)),
	}
	for i, l := range spec.Layouts {
		seen := make(map[string]struct{})
		root, err := buildPane(ctx, l.Root, resolver, opts, seen)
		if err != nil {
			return muxctl.MuxSpec{}, fmt.Errorf("layouts[%d] (%q): %w", i, l.Name, err)
		}
		out.Layouts = append(out.Layouts, muxctl.Layout{Name: l.Name, Root: root})
	}
	if err := out.Validate(); err != nil {
		return muxctl.MuxSpec{}, err
	}
	return out, nil
}

func buildPane(
	ctx context.Context,
	p PaneSpec,
	resolver Resolver,
	opts PaneArgvOpts,
	seen map[string]struct{},
) (muxctl.PaneSpec, error) {
	switch {
	case p.IsLeaf():
		if _, dup := seen[p.Command]; dup {
			return muxctl.PaneSpec{}, fmt.Errorf("duplicate command %q in layout", p.Command)
		}
		seen[p.Command] = struct{}{}
		id, err := resolver(ctx, p.Command)
		if err != nil {
			return muxctl.PaneSpec{}, fmt.Errorf("resolve leaf %q: %w", p.Command, err)
		}
		return muxctl.PaneSpec{
			Leaf: muxctl.Leaf{
				Name:   p.Command,
				Cmd:    paneArgv(opts, p.Mode, id),
				CmdOpt: p.CmdOpt,
				Focus:  p.Focus,
			},
		}, nil
	case p.IsContainer():
		children := make([]muxctl.PaneSpec, len(p.Panes))
		for i, child := range p.Panes {
			built, err := buildPane(ctx, child, resolver, opts, seen)
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

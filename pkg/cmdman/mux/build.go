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
// [Build] uses it only to size the replica cycle for leaves that pin no scale
// index. A nil ReplicaCounter disables cycling: every unpinned leaf resolves
// once at scale index 0 (the standalone `cmdman mux` behavior).
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

// Build resolves each leaf's command via resolver and emits a [muxctl.MuxSpec]
// with concrete argv.
//
// A layout containing leaves that pin no scale index but reference scaled
// commands is expanded into one muxctl layout per replica-cycle position: the
// first keeps the layout's name, the rest are suffixed "#<n>". Because muxctl
// cycles layouts on successive `mux` invocations, this makes those invocations
// rotate through the replicas — while an unscaled spec expands one-to-one and
// behaves exactly as before. replicas may be nil to disable that expansion.
//
// Duplicate resolved commands within a single realized layout are rejected (one
// pane per command replica).
func Build(
	ctx context.Context,
	spec Spec,
	resolver Resolver,
	replicas ReplicaCounter,
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
		expanded, err := buildLayout(ctx, l, resolver, replicas, opts)
		if err != nil {
			return muxctl.MuxSpec{}, fmt.Errorf("layouts[%d] (%q): %w", i, l.Name, err)
		}
		out.Layouts = append(out.Layouts, expanded...)
	}
	if err := out.Validate(); err != nil {
		return muxctl.MuxSpec{}, err
	}
	return out, nil
}

// buildLayout expands one cmdman-layer layout into one or more muxctl layouts,
// one per replica-cycle position (see [Build]). counts caches the replica count
// of every cycling leaf so the cycle length is the largest among them.
func buildLayout(
	ctx context.Context,
	l Layout,
	resolver Resolver,
	replicas ReplicaCounter,
	opts PaneArgvOpts,
) ([]muxctl.Layout, error) {
	counts, err := cyclingReplicaCounts(ctx, l.Root, replicas)
	if err != nil {
		return nil, err
	}
	cycleLen := 1
	for _, n := range counts {
		cycleLen = max(cycleLen, n)
	}

	layouts := make([]muxctl.Layout, 0, cycleLen)
	for scalePos := range cycleLen {
		seen := make(map[string]struct{})
		root, err := buildPane(ctx, l.Root, resolver, opts, seen, scalePos, counts)
		if err != nil {
			return nil, err
		}
		name := l.Name
		if scalePos > 0 {
			name = fmt.Sprintf("%s#%d", l.Name, scalePos+1)
		}
		layouts = append(layouts, muxctl.Layout{Name: name, Root: root})
	}
	return layouts, nil
}

// cyclingReplicaCounts returns the replica count of every leaf in the tree that
// pins no scale index, keyed by command name. With a nil ReplicaCounter (no
// cycling) it returns an empty map, so the layout expands to a single position.
func cyclingReplicaCounts(
	ctx context.Context,
	p PaneSpec,
	replicas ReplicaCounter,
) (map[string]int, error) {
	counts := make(map[string]int)
	if replicas == nil {
		return counts, nil
	}
	var walk func(PaneSpec) error
	walk = func(p PaneSpec) error {
		switch {
		case p.IsLeaf():
			if p.Scale > 0 {
				return nil // explicitly pinned: does not cycle
			}
			if _, done := counts[p.Command]; done {
				return nil
			}
			n, err := replicas(ctx, p.Command)
			if err != nil {
				return fmt.Errorf("count replicas of %q: %w", p.Command, err)
			}
			counts[p.Command] = max(n, 1)
		case p.IsContainer():
			for _, child := range p.Panes {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(p); err != nil {
		return nil, err
	}
	return counts, nil
}

func buildPane(
	ctx context.Context,
	p PaneSpec,
	resolver Resolver,
	opts PaneArgvOpts,
	seen map[string]struct{},
	scalePos int,
	counts map[string]int,
) (muxctl.PaneSpec, error) {
	switch {
	case p.IsLeaf():
		// Resolve the concrete replica: an explicit Scale pins it; otherwise the
		// cycle position selects one (mod the command's replica count), or 0 when
		// the command is not scaled / cycling is disabled.
		idx := p.Scale
		scaled := p.Scale > 0
		if idx <= 0 {
			if n, cycling := counts[p.Command]; cycling {
				idx = (scalePos % n) + 1
				scaled = n > 1
			}
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

		name := p.Command
		if scaled {
			name = fmt.Sprintf("%s-%d", p.Command, idx)
		}
		return muxctl.PaneSpec{
			Leaf: muxctl.Leaf{
				Name:   name,
				Cmd:    paneArgv(opts, p.Mode, id),
				CmdOpt: p.CmdOpt,
				Focus:  p.Focus,
			},
		}, nil
	case p.IsContainer():
		children := make([]muxctl.PaneSpec, len(p.Panes))
		for i, child := range p.Panes {
			built, err := buildPane(ctx, child, resolver, opts, seen, scalePos, counts)
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

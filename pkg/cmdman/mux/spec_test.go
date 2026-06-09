package mux_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

const sampleSpec = `
mux:
  driver: tmux
  driver_opt:
    socket: cmdman
  layouts:
    - name: services
      root:
        dir: h
        splits: [90c, 1, 2]
        panes:
          - api
          - command: worker
            mode: logs
            cmd_opt:
              title: w
            focus: true
          - dir: v
            splits: [1, 1]
            panes:
              - redis
              - db
`

func TestDecodeAcceptsBareLeafShorthand(t *testing.T) {
	t.Parallel()

	spec, err := mux.Decode(strings.NewReader(sampleSpec))
	assert.NilError(t, err)
	assert.Equal(t, spec.Driver, "tmux")
	assert.Equal(t, spec.DriverOpt["socket"], "cmdman")
	assert.Equal(t, len(spec.Layouts), 1)

	root := spec.Layouts[0].Root
	assert.Equal(t, root.Dir, mux.DirHorizontal)
	assert.Equal(t, len(root.Panes), 3)

	// First pane: bare-string shorthand becomes a leaf with Command set.
	assert.Equal(t, root.Panes[0].Command, "api")
	assert.Equal(t, root.Panes[0].Mode, mux.Mode(""))
	assert.Assert(t, root.Panes[0].IsLeaf())

	// Second pane: full mapping form with mode/cmd_opt/focus.
	assert.Equal(t, root.Panes[1].Command, "worker")
	assert.Equal(t, root.Panes[1].Mode, mux.ModeLogs)
	assert.Equal(t, root.Panes[1].CmdOpt["title"], "w")
	assert.Assert(t, root.Panes[1].Focus)

	// Third pane: nested container with two bare-string leaves.
	nested := root.Panes[2]
	assert.Assert(t, nested.IsContainer())
	assert.Equal(t, nested.Dir, mux.DirVertical)
	assert.Equal(t, len(nested.Panes), 2)
	assert.Equal(t, nested.Panes[0].Command, "redis")
	assert.Equal(t, nested.Panes[1].Command, "db")
}

func TestDecodeRejectsMissingMuxKey(t *testing.T) {
	t.Parallel()

	_, err := mux.Decode(strings.NewReader("driver: tmux\n"))
	assert.ErrorContains(t, err, `"mux:" key`)
}

func TestBuildResolvesArgvAndPropagatesFields(t *testing.T) {
	t.Parallel()

	spec, err := mux.Decode(strings.NewReader(sampleSpec))
	assert.NilError(t, err)

	resolver := func(_ context.Context, name string, _ int) (string, error) {
		return "id-" + name, nil
	}
	built, err := mux.Build(context.Background(), spec, resolver, nil, mux.PaneArgvOpts{
		Executable: "/usr/bin/cmdman",
		DataDir:    "/var/lib/cmdman",
		RuntimeDir: "/run/cmdman",
	})
	assert.NilError(t, err)

	assert.Equal(t, len(built.Layouts), 1)
	root := built.Layouts[0].Root

	// api: bare-string shorthand → attach argv.
	api := root.Panes[0].Leaf
	assert.Equal(t, api.Name, "api")
	assert.DeepEqual(t, api.Cmd, []string{
		"/usr/bin/cmdman",
		"--data-dir", "/var/lib/cmdman",
		"--runtime-dir", "/run/cmdman",
		"attach", "id-api",
	})

	// worker: ModeLogs → logs --sticky argv. cmd_opt + focus propagate.
	worker := root.Panes[1].Leaf
	assert.DeepEqual(t, worker.Cmd, []string{
		"/usr/bin/cmdman",
		"--data-dir", "/var/lib/cmdman",
		"--runtime-dir", "/run/cmdman",
		"logs", "--sticky", "id-worker",
	})
	assert.Equal(t, worker.CmdOpt["title"], "w")
	assert.Assert(t, worker.Focus)

	// Container fields cross over.
	assert.Equal(t, root.Dir, muxctl.DirHorizontal)
	assert.Equal(t, len(root.Splits), 3)
	assert.Equal(t, root.Splits[0], muxctl.Size{N: 90, Absolute: true})
}

func TestBuildRejectsDuplicateLeafCommand(t *testing.T) {
	t.Parallel()

	const dupSpec = `
mux:
  layouts:
    - name: l
      root:
        dir: h
        splits: [1, 1]
        panes: [api, api]
`
	spec, err := mux.Decode(strings.NewReader(dupSpec))
	assert.NilError(t, err)

	_, err = mux.Build(
		context.Background(),
		spec,
		func(_ context.Context, name string, _ int) (string, error) { return "id-" + name, nil },
		nil,
		mux.PaneArgvOpts{Executable: "/usr/bin/cmdman"},
	)
	assert.ErrorContains(t, err, `duplicate command "api"`)
}

func TestBuildSurfacesResolverError(t *testing.T) {
	t.Parallel()

	const oneLeaf = `
mux:
  layouts:
    - name: l
      root: { command: ghost }
`
	spec, err := mux.Decode(strings.NewReader(oneLeaf))
	assert.NilError(t, err)

	wantErr := "not found: ghost"
	_, err = mux.Build(
		context.Background(),
		spec,
		func(_ context.Context, name string, _ int) (string, error) {
			return "", &resolveErr{name: name}
		},
		nil,
		mux.PaneArgvOpts{Executable: "/usr/bin/cmdman"},
	)
	assert.ErrorContains(t, err, wantErr)
}

type resolveErr struct{ name string }

func (e *resolveErr) Error() string { return "not found: " + e.name }

// TestBuildExpandsUnpinnedScaledLeafIntoCycle verifies that a leaf referencing a
// scaled command with no pinned scale index expands the layout into one muxctl
// layout per replica, so layout cycling rotates the replica shown.
func TestBuildExpandsUnpinnedScaledLeafIntoCycle(t *testing.T) {
	t.Parallel()

	const oneLeaf = `
mux:
  layouts:
    - name: dash
      root: { command: web }
`
	spec, err := mux.Decode(strings.NewReader(oneLeaf))
	assert.NilError(t, err)

	resolver := func(_ context.Context, name string, idx int) (string, error) {
		return fmt.Sprintf("id-%s-%d", name, idx), nil
	}
	replicas := func(_ context.Context, name string) (int, error) {
		if name == "web" {
			return 3, nil
		}
		return 1, nil
	}

	built, err := mux.Build(
		context.Background(), spec, resolver, replicas,
		mux.PaneArgvOpts{Executable: "/cmdman"},
	)
	assert.NilError(t, err)

	// One layout per replica: "dash", "dash#2", "dash#3".
	assert.Equal(t, len(built.Layouts), 3)
	assert.Equal(t, built.Layouts[0].Name, "dash")
	assert.Equal(t, built.Layouts[1].Name, "dash#2")
	assert.Equal(t, built.Layouts[2].Name, "dash#3")

	// Each cycle position resolves the corresponding replica id and names the
	// pane with its scale-index suffix.
	for i, want := range []string{"id-web-1", "id-web-2", "id-web-3"} {
		leaf := built.Layouts[i].Root.Leaf
		assert.Equal(t, leaf.Name, fmt.Sprintf("web-%d", i+1))
		assert.DeepEqual(t, leaf.Cmd, []string{"/cmdman", "attach", want})
	}
}

// TestBuildPinnedScaleIndexDoesNotCycle verifies that a leaf pinning an explicit
// scale index resolves exactly that replica and does not expand the layout.
func TestBuildPinnedScaleIndexDoesNotCycle(t *testing.T) {
	t.Parallel()

	const pinned = `
mux:
  layouts:
    - name: dash
      root: { command: web, scale: 2 }
`
	spec, err := mux.Decode(strings.NewReader(pinned))
	assert.NilError(t, err)

	resolver := func(_ context.Context, name string, idx int) (string, error) {
		return fmt.Sprintf("id-%s-%d", name, idx), nil
	}
	replicas := func(_ context.Context, _ string) (int, error) { return 3, nil }

	built, err := mux.Build(
		context.Background(), spec, resolver, replicas,
		mux.PaneArgvOpts{Executable: "/cmdman"},
	)
	assert.NilError(t, err)

	// A pinned leaf does not cycle: a single layout, resolving replica 2.
	assert.Equal(t, len(built.Layouts), 1)
	leaf := built.Layouts[0].Root.Leaf
	assert.Equal(t, leaf.Name, "web-2")
	assert.DeepEqual(t, leaf.Cmd, []string{"/cmdman", "attach", "id-web-2"})
}

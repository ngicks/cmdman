package compose_test

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// TestEmbeddedMuxLayoutParsed verifies that a compose file's "mux:" section is
// eagerly decoded into the typed ComposeSpec.Mux (*mux.Spec) at load time,
// preserving the full cmdman-layer grammar: bare-string and full-mapping
// leaves, mode/cmd_opt/focus, nested containers, the size grammar, and
// multiple switchable layouts. Leaves keep their project-scoped service names;
// resolution to commands happens later, in `cmdman compose mux`.
func TestEmbeddedMuxLayoutParsed(t *testing.T) {
	spec, err := normalizeFromFile(t, testdataPath("mux.yaml"), compose.NormalizeOpts{})
	assert.NilError(t, err)

	assert.Assert(t, spec.Mux != nil, "embedded mux section should be decoded")
	assert.Equal(t, spec.Mux.Driver, "tmux")
	assert.Equal(t, spec.Mux.DriverOpt["socket"], "cmdman")
	assert.Equal(t, len(spec.Mux.Layouts), 2)

	// Layout "services".
	services := spec.Mux.Layouts[0]
	assert.Equal(t, services.Name, "services")
	root := services.Root
	assert.Equal(t, root.Dir, mux.DirHorizontal)
	assert.Equal(t, len(root.Panes), 3)

	// Size grammar survives: percent, then bare weights.
	assert.DeepEqual(t, root.Splits, []muxctl.Size{
		{N: 50, Percent: true},
		{N: 1},
		{N: 1},
	})

	// pane[0]: bare-string shorthand leaf (service name, unresolved).
	assert.Assert(t, root.Panes[0].IsLeaf())
	assert.Equal(t, root.Panes[0].Command, "api")
	assert.Equal(t, root.Panes[0].Mode, mux.Mode(""))

	// pane[1]: full-mapping leaf with mode/cmd_opt/focus.
	assert.Assert(t, root.Panes[1].IsLeaf())
	assert.Equal(t, root.Panes[1].Command, "worker")
	assert.Equal(t, root.Panes[1].Mode, mux.ModeLogs)
	assert.Equal(t, root.Panes[1].CmdOpt["title"], "Worker")
	assert.Assert(t, root.Panes[1].Focus)

	// pane[2]: nested vertical container with two bare-string leaves.
	nested := root.Panes[2]
	assert.Assert(t, nested.IsContainer())
	assert.Equal(t, nested.Dir, mux.DirVertical)
	assert.Equal(t, len(nested.Panes), 2)
	assert.Equal(t, nested.Panes[0].Command, "redis")
	assert.Equal(t, nested.Panes[1].Command, "db")

	// Layout "stacked": absolute "Nc" + weight.
	stacked := spec.Mux.Layouts[1]
	assert.Equal(t, stacked.Name, "stacked")
	assert.Equal(t, stacked.Root.Dir, mux.DirVertical)
	assert.DeepEqual(t, stacked.Root.Splits, []muxctl.Size{
		{N: 10, Absolute: true},
		{N: 1},
	})
}

// TestEmbeddedMuxAbsent verifies that a compose file with no "mux:" section
// leaves ComposeSpec.Mux nil (the absence sentinel consumers check).
func TestEmbeddedMuxAbsent(t *testing.T) {
	spec, err := normalizeFromFile(t, testdataPath("basic.yaml"), compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Assert(t, spec.Mux == nil, "no mux: section should leave Mux nil")
}

// TestEmbeddedMuxMalformedFailsAtLoad documents the consequence of eager
// decoding: a structurally invalid "mux:" section (here an unparseable size)
// fails at compose load, not only when `compose mux` runs.
func TestEmbeddedMuxMalformedFailsAtLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd-compose.yaml")
	content := `name: badmux
commands:
  api:
    args: [echo, hi]
mux:
  layouts:
    - name: broken
      root:
        dir: h
        splits: [notasize]
        panes: [api]
`
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))

	_, err := compose.DecodeFile(path)
	assert.ErrorContains(t, err, "size")
}

package mux_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

// TestBuildEmitsOneLayoutPerSpecLayout verifies that Build emits exactly one
// muxctl layout per spec layout, regardless of replica counts.
func TestBuildEmitsOneLayoutPerSpecLayout(t *testing.T) {
	t.Parallel()

	const twoLayouts = `
mux:
  layouts:
    - name: web-view
      root: { command: web }
    - name: worker-view
      root: { command: worker }
`
	spec, err := mux.Decode(mustReader(twoLayouts))
	assert.NilError(t, err)

	replicas := func(_ context.Context, name string) (int, error) {
		switch name {
		case "web":
			return 3, nil
		case "worker":
			return 2, nil
		}
		return 1, nil
	}

	built, err := mux.Build(context.Background(), mux.BuildOptions{
		Spec:     spec,
		Resolver: indexResolver,
		Replicas: replicas,
		Opts:     mux.PaneArgvOpts{Executable: "/cmdman"},
	})
	assert.NilError(t, err)
	assert.Equal(t, len(built.Layouts), 2)
}

// TestBuildScalePositionsMissingKeyDefaultsTo1 verifies that when ScalePositions
// is nil and a leaf has 3 replicas, it resolves replica 1 and names the pane
// "web-1" with CycleKey "web".
func TestBuildScalePositionsMissingKeyDefaultsTo1(t *testing.T) {
	t.Parallel()

	const oneLeaf = `
mux:
  layouts:
    - name: dash
      root: { command: web }
`
	spec, err := mux.Decode(mustReader(oneLeaf))
	assert.NilError(t, err)

	replicas := func(_ context.Context, _ string) (int, error) { return 3, nil }

	built, err := mux.Build(context.Background(), mux.BuildOptions{
		Spec:           spec,
		Resolver:       indexResolver,
		Replicas:       replicas,
		Opts:           mux.PaneArgvOpts{Executable: "/cmdman"},
		ScalePositions: nil,
	})
	assert.NilError(t, err)

	leaf := built.Layouts[0].Root.Leaf
	assert.Equal(t, leaf.Name, "web-1")
	assert.DeepEqual(t, leaf.Cmd, []string{"/cmdman", "attach", "id-web-1"})
	assert.Equal(t, leaf.CycleKey, "web")
}

// TestBuildScalePositionsWrap verifies that a stored position exceeding the
// replica count wraps: ScalePositions={"web":5}, 3 replicas →
// pos = ((5-1)%3)+1 = 2 → resolves replica 2, name "web-2", CycleKey "web".
func TestBuildScalePositionsWrap(t *testing.T) {
	t.Parallel()

	const oneLeaf = `
mux:
  layouts:
    - name: dash
      root: { command: web }
`
	spec, err := mux.Decode(mustReader(oneLeaf))
	assert.NilError(t, err)

	replicas := func(_ context.Context, _ string) (int, error) { return 3, nil }

	built, err := mux.Build(context.Background(), mux.BuildOptions{
		Spec:           spec,
		Resolver:       indexResolver,
		Replicas:       replicas,
		Opts:           mux.PaneArgvOpts{Executable: "/cmdman"},
		ScalePositions: map[string]int{"web": 5},
	})
	assert.NilError(t, err)

	leaf := built.Layouts[0].Root.Leaf
	assert.Equal(t, leaf.Name, "web-2")
	assert.DeepEqual(t, leaf.Cmd, []string{"/cmdman", "attach", "id-web-2"})
	assert.Equal(t, leaf.CycleKey, "web")
}

// TestBuildCycleKeySetOnlyOnUnpinnedCyclingLeaves verifies:
//   - Unpinned leaf "web" with 3 replicas and a non-nil ReplicaCounter → CycleKey == "web"
//   - Pinned leaf (scale: 2) → CycleKey == ""
//   - Nil ReplicaCounter, unpinned leaf → CycleKey == ""
func TestBuildCycleKeySetOnlyOnUnpinnedCyclingLeaves(t *testing.T) {
	t.Parallel()

	replicas3 := func(_ context.Context, _ string) (int, error) { return 3, nil }

	// Unpinned with replicas → CycleKey set.
	t.Run("unpinned cycling", func(t *testing.T) {
		t.Parallel()

		const oneLeaf = `
mux:
  layouts:
    - name: dash
      root: { command: web }
`
		spec, err := mux.Decode(mustReader(oneLeaf))
		assert.NilError(t, err)

		built, err := mux.Build(context.Background(), mux.BuildOptions{
			Spec:     spec,
			Resolver: indexResolver,
			Replicas: replicas3,
			Opts:     mux.PaneArgvOpts{Executable: "/cmdman"},
		})
		assert.NilError(t, err)
		assert.Equal(t, built.Layouts[0].Root.CycleKey, "web")
	})

	// Pinned leaf → CycleKey empty.
	t.Run("pinned", func(t *testing.T) {
		t.Parallel()

		const pinned = `
mux:
  layouts:
    - name: dash
      root: { command: web, scale: 2 }
`
		spec, err := mux.Decode(mustReader(pinned))
		assert.NilError(t, err)

		built, err := mux.Build(context.Background(), mux.BuildOptions{
			Spec:     spec,
			Resolver: indexResolver,
			Replicas: replicas3,
			Opts:     mux.PaneArgvOpts{Executable: "/cmdman"},
		})
		assert.NilError(t, err)
		assert.Equal(t, built.Layouts[0].Root.CycleKey, "")
	})

	// Nil ReplicaCounter → CycleKey empty.
	t.Run("nil replicas", func(t *testing.T) {
		t.Parallel()

		const oneLeaf = `
mux:
  layouts:
    - name: dash
      root: { command: web }
`
		spec, err := mux.Decode(mustReader(oneLeaf))
		assert.NilError(t, err)

		built, err := mux.Build(context.Background(), mux.BuildOptions{
			Spec:     spec,
			Resolver: indexResolver,
			Replicas: nil,
			Opts:     mux.PaneArgvOpts{Executable: "/cmdman"},
		})
		assert.NilError(t, err)
		assert.Equal(t, built.Layouts[0].Root.CycleKey, "")
	})
}

// TestBuildNilReplicasCounterUnpinnedResolvesAtIndex0 verifies that with a nil
// ReplicaCounter an unpinned leaf resolves at scaleIndex 0, gets no name suffix,
// and CycleKey is empty.
func TestBuildNilReplicasCounterUnpinnedResolvesAtIndex0(t *testing.T) {
	t.Parallel()

	const oneLeaf = `
mux:
  layouts:
    - name: dash
      root: { command: web }
`
	spec, err := mux.Decode(mustReader(oneLeaf))
	assert.NilError(t, err)

	var gotIndex int
	resolver := func(_ context.Context, _ string, idx int) (string, error) {
		gotIndex = idx
		return fmt.Sprintf("id-web-%d", idx), nil
	}

	built, err := mux.Build(context.Background(), mux.BuildOptions{
		Spec:     spec,
		Resolver: resolver,
		Replicas: nil,
		Opts:     mux.PaneArgvOpts{Executable: "/cmdman"},
	})
	assert.NilError(t, err)
	assert.Equal(t, gotIndex, 0)

	leaf := built.Layouts[0].Root.Leaf
	assert.Equal(t, leaf.Name, "web")
	assert.Equal(t, leaf.CycleKey, "")
}

// indexResolver resolves "id-<name>-<idx>" for a positive index, "id-<name>-0"
// for index 0.
func indexResolver(_ context.Context, name string, idx int) (string, error) {
	return fmt.Sprintf("id-%s-%d", name, idx), nil
}

// mustReader wraps a YAML string as an io.Reader.
func mustReader(s string) *strings.Reader {
	return strings.NewReader(s)
}

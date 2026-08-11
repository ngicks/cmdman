package mux

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

// TestComputeTargetPosition covers the advance-by-one and explicit-position
// branches of computeTargetPosition.
func TestComputeTargetPosition(t *testing.T) {
	t.Parallel()

	noPinned := map[int]struct{}{}

	tests := []struct {
		name        string
		curPos      int
		explicitPos int
		n           int
		pinned      map[int]struct{}
		wantPos     int
		wantErrSub  string // non-empty → expect an error containing this substring
	}{
		{
			name:   "advance wraps: curPos=3, n=3 → 1",
			curPos: 3, explicitPos: 0, n: 3,
			pinned:  noPinned,
			wantPos: 1,
		},
		{
			name:   "advance skips pinned: curPos=1, n=3, pinned={2} → 3",
			curPos: 1, explicitPos: 0, n: 3,
			pinned:  map[int]struct{}{2: {}},
			wantPos: 3,
		},
		{
			name:   "all-pinned returns error",
			curPos: 1, explicitPos: 0, n: 3,
			pinned:  map[int]struct{}{1: {}, 2: {}, 3: {}},
			wantPos: 0, wantErrSub: "all scale positions",
		},
		{
			name:   "explicit out-of-range high returns error",
			curPos: 1, explicitPos: 5, n: 3,
			pinned:  noPinned,
			wantPos: 0, wantErrSub: "out of range",
		},
		{
			name:   "advance baseline: curPos=1, n=3 → 2",
			curPos: 1, explicitPos: 0, n: 3,
			pinned:  noPinned,
			wantPos: 2, // advance-by-one: (1%3)+1 = 2
		},
		{
			name:   "explicit out-of-range low returns error",
			curPos: 1, explicitPos: -1, n: 3,
			pinned:  noPinned,
			wantPos: 0, wantErrSub: "out of range",
		},
		{
			name:   "explicit pinned returns error",
			curPos: 1, explicitPos: 2, n: 3,
			pinned:  map[int]struct{}{2: {}},
			wantPos: 0, wantErrSub: "pinned",
		},
		{
			name:   "explicit in-range not-pinned returns position",
			curPos: 1, explicitPos: 2, n: 3,
			pinned:  noPinned,
			wantPos: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := computeTargetPosition(tc.curPos, tc.explicitPos, tc.n, tc.pinned)
			if tc.wantErrSub != "" {
				assert.ErrorContains(t, err, tc.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tc.wantPos)
		})
	}
}

// TestIsCycleScaleTarget covers the boolean predicate isCycleScaleTarget.
func TestIsCycleScaleTarget(t *testing.T) {
	t.Parallel()

	// A Spec with two layouts:
	// - "web-view" has an unpinned "web" leaf (Scale == 0)
	// - "worker-view" has a pinned "web" leaf (Scale == 2) and an unpinned "worker" leaf
	makeSpec := func() Spec {
		return Spec{
			Layouts: []Layout{
				{
					Name: "web-view",
					Root: PaneSpec{Command: "web", Scale: 0},
				},
				{
					Name: "worker-view",
					Root: PaneSpec{
						Dir: DirHorizontal,
						Panes: []PaneSpec{
							{Command: "web", Scale: 2},
							{Command: "worker", Scale: 0},
						},
					},
				},
			},
		}
	}

	dummyReplicas := ReplicaCounter(func(_ context.Context, _ string) (int, error) {
		return 3, nil
	})

	tests := []struct {
		name     string
		spec     Spec
		command  string
		replicas ReplicaCounter
		want     bool
	}{
		{
			name:     "command with unpinned leaf and non-nil replicas → true",
			spec:     makeSpec(),
			command:  "web",
			replicas: dummyReplicas,
			want:     true,
		},
		{
			name: "command only appears pinned (Scale>0) in all layouts → false",
			spec: Spec{
				Layouts: []Layout{
					{
						Name: "only-pinned",
						Root: PaneSpec{Command: "web", Scale: 1},
					},
				},
			},
			command:  "web",
			replicas: dummyReplicas,
			want:     false,
		},
		{
			name:     "nil replicas → false",
			spec:     makeSpec(),
			command:  "web",
			replicas: nil,
			want:     false,
		},
		{
			name:     "command not in spec at all → false",
			spec:     makeSpec(),
			command:  "nonexistent",
			replicas: dummyReplicas,
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := isCycleScaleTarget(tc.spec, tc.command, tc.replicas)
			assert.Equal(t, got, tc.want)
		})
	}
}

// TestPinnedScaleIndices verifies that pinnedScaleIndices collects only the
// Scale values > 0 for the named command in a layout.
func TestPinnedScaleIndices(t *testing.T) {
	t.Parallel()

	// Layout: root container with three leaves:
	//   web (Scale=2), web (Scale=3), worker (Scale=1)
	layout := Layout{
		Name: "mixed",
		Root: PaneSpec{
			Dir: DirHorizontal,
			Panes: []PaneSpec{
				{Command: "web", Scale: 2},
				{Command: "web", Scale: 3},
				{Command: "worker", Scale: 1},
			},
		},
	}

	t.Run("web pinned at 2 and 3", func(t *testing.T) {
		t.Parallel()

		got := pinnedScaleIndices(layout, "web")
		assert.Equal(t, len(got), 2)
		_, has2 := got[2]
		_, has3 := got[3]
		assert.Equal(t, has2, true)
		assert.Equal(t, has3, true)
	})

	t.Run("worker pinned at 1", func(t *testing.T) {
		t.Parallel()

		got := pinnedScaleIndices(layout, "worker")
		assert.Equal(t, len(got), 1)
		_, has1 := got[1]
		assert.Equal(t, has1, true)
	})

	t.Run("unknown command returns empty map", func(t *testing.T) {
		t.Parallel()

		got := pinnedScaleIndices(layout, "api")
		assert.Equal(t, len(got), 0)
	})

	t.Run("unpinned leaf not collected", func(t *testing.T) {
		// A layout where the web leaf has Scale=0 (unpinned) — should not appear.
		t.Parallel()

		unpinned := Layout{
			Name: "unpinned",
			Root: PaneSpec{Command: "web", Scale: 0},
		}
		got := pinnedScaleIndices(unpinned, "web")
		assert.Equal(t, len(got), 0)
	})
}

// TestCycleScaleNotATarget verifies that CycleScale returns an error containing
// "not a cycle-scale target" when the provided Command does not appear as an
// unpinned leaf in any layout.
func TestCycleScaleNotATarget(t *testing.T) {
	t.Parallel()

	// Spec where "web" only appears pinned — never qualifies as a cycle-scale target.
	spec := Spec{
		Layouts: []Layout{
			{
				Name: "pinned-only",
				Root: PaneSpec{Command: "web", Scale: 2},
			},
		},
	}

	dummyReplicas := ReplicaCounter(func(_ context.Context, _ string) (int, error) {
		return 3, nil
	})

	_, err := CycleScale(context.Background(), CycleScaleOptions{
		Spec:     spec,
		Replicas: dummyReplicas,
		Command:  "web",
	})
	assert.ErrorContains(t, err, "not a cycle-scale target")
}

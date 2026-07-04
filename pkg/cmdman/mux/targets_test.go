package mux_test

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

func TestCollectCycleTargets(t *testing.T) {
	t.Run("empty spec yields nil", func(t *testing.T) {
		got := mux.CollectCycleTargets(mux.Spec{})
		assert.Assert(t, got == nil)
	})

	t.Run("sorted, deduplicated across layouts, pinned leaves excluded", func(t *testing.T) {
		spec := mux.Spec{
			Layouts: []mux.Layout{
				{
					Name: "one",
					Root: mux.PaneSpec{
						Dir: mux.DirHorizontal,
						Panes: []mux.PaneSpec{
							{Command: "web"},          // unpinned target
							{Command: "db", Scale: 1}, // pinned, excluded
							{Command: "api"},          // unpinned target
						},
					},
				},
				{
					Name: "two",
					Root: mux.PaneSpec{
						Dir: mux.DirVertical,
						Panes: []mux.PaneSpec{
							{Command: "web"}, // duplicate across layouts
							{
								Dir: mux.DirHorizontal,
								Panes: []mux.PaneSpec{
									{Command: "worker"}, // nested unpinned target
								},
							},
						},
					},
				},
			},
		}
		got := mux.CollectCycleTargets(spec)
		assert.DeepEqual(t, got, []string{"api", "web", "worker"})
	})

	t.Run("only pinned leaves yields nil", func(t *testing.T) {
		spec := mux.Spec{
			Layouts: []mux.Layout{
				{
					Name: "pinned",
					Root: mux.PaneSpec{
						Dir: mux.DirHorizontal,
						Panes: []mux.PaneSpec{
							{Command: "web", Scale: 1},
							{Command: "web", Scale: 2},
						},
					},
				},
			},
		}
		got := mux.CollectCycleTargets(spec)
		assert.Assert(t, got == nil)
	})
}

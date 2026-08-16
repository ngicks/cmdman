package cli

import (
	"testing"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
)

// pmTestSpec is a project whose "web" leaf is unpinned (a cycle target) while
// "db" is pinned to a replica and "tool" appears in no layout at all.
func pmTestSpec() *compose.ComposeSpec {
	return &compose.ComposeSpec{
		Project: "proj",
		WorkDir: "/work",
		Commands: []compose.Command{
			{Name: "web", Scale: 3},
			{Name: "db"},
			{Name: "tool"},
		},
		Mux: &mux.Spec{
			Layouts: []mux.Layout{
				{
					Name: "dev",
					Root: mux.PaneSpec{
						Dir: "h",
						Panes: []mux.PaneSpec{
							{Command: "web"},
							{Command: "db", Scale: 1},
						},
					},
				},
			},
		},
	}
}

// The rows follow the compose file's command order, carry the store's replica
// count (D11), and mark only the leaves cycle-scale can advance.
func TestServiceScaleInfosProjectsEveryCommand(t *testing.T) {
	got := serviceScaleInfos(
		pmTestSpec(),
		map[string]int{"web": 3, "db": 1},
		map[string]int{"web": 2},
	)
	if len(got) != 3 {
		t.Fatalf("expected one row per compose command, got %d: %+v", len(got), got)
	}
	if got[0].Name != "web" || got[1].Name != "db" || got[2].Name != "tool" {
		t.Fatalf("rows should follow definition order; got %+v", got)
	}
	if got[0].Replicas != 3 || got[0].Shown != 2 || !got[0].Cyclable {
		t.Errorf("unpinned scaled leaf = %+v", got[0])
	}
	if got[1].Cyclable {
		t.Errorf("a pinned leaf is not a cycle target: %+v", got[1])
	}
	if got[2].Cyclable {
		t.Errorf("a command in no layout is not a cycle target: %+v", got[2])
	}
}

// A service the store knows nothing about reads as zero replicas, and one no
// window agrees on (D14) reads as an unknown shown replica — neither is an
// error, and neither may borrow another row's number.
func TestServiceScaleInfosUnknownsAreZero(t *testing.T) {
	got := serviceScaleInfos(pmTestSpec(), map[string]int{"web": 3}, nil)
	if got[1].Replicas != 0 {
		t.Errorf("a never-created service should read 0 replicas; got %+v", got[1])
	}
	for _, info := range got {
		if info.Shown != 0 {
			t.Errorf("no agreed position means unknown; got %+v", info)
		}
	}
}

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestResolveManagerSelectionPrefersTheExplicitTarget is D17: a panel invoked
// with --file/--project-name manages that project and asks no probe about it —
// the summon names the row the cursor was on, and a popup always opens inside
// some window whose project would otherwise win.
func TestResolveManagerSelectionPrefersTheExplicitTarget(t *testing.T) {
	outsideMux(t)
	t.Setenv("CMDMAN_CONF", filepath.Join(t.TempDir(), "config.json"))

	here := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(here, "cmd-compose.yaml"),
		[]byte(strings.Replace(muxComposeYAML, "name: tools", "name: here", 1)),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	target := filepath.Join(elsewhere, "cmd-compose.yaml")
	if err := os.WriteFile(
		target,
		[]byte(strings.Replace(muxComposeYAML, "name: tools", "name: elsewhere", 1)),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	// The cwd probe would answer, and answer with the other project.
	t.Chdir(here)

	ctx := context.Background()
	explicit := &serviceBackend{file: target, projectName: "elsewhere"}
	sel, err := explicit.resolveManagerSelection(ctx, "", "")
	if err != nil {
		t.Fatalf("explicit target: %v", err)
	}
	if sel.Project != "elsewhere" {
		t.Errorf("explicit target resolved %q, want elsewhere", sel.Project)
	}

	// Without one, the ambient chain is untouched: the cwd project is the answer.
	ambient := &serviceBackend{}
	sel, err = ambient.resolveManagerSelection(ctx, "", "")
	if err != nil {
		t.Fatalf("ambient: %v", err)
	}
	if sel.Project != "here" {
		t.Errorf("bare invocation resolved %q, want the cwd project here", sel.Project)
	}
}

// TestResolveManagerSelectionFailsOnAnUnresolvableTarget: an explicit target
// that does not resolve is the answer, not a reason to go looking for another
// project — the caller named one.
func TestResolveManagerSelectionFailsOnAnUnresolvableTarget(t *testing.T) {
	outsideMux(t)
	t.Setenv("CMDMAN_CONF", filepath.Join(t.TempDir(), "config.json"))

	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "cmd-compose.yaml"), []byte(muxComposeYAML), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	b := &serviceBackend{file: filepath.Join(dir, "no-such-compose.yaml")}
	if _, err := b.resolveManagerSelection(context.Background(), "", ""); err == nil {
		t.Fatal("a named target that does not resolve should fail, not fall back")
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

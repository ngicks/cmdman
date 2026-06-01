package compose_test

import (
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func TestListMuxProjects(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	composeDir := filepath.Join(conf, "compose")

	muxYAML := func(name string) string {
		return "name: " + name + `
commands:
  a:
    args: [echo, a]
mux:
  driver: tmux
  layouts:
    - name: solo
      root: a
`
	}

	// Two projects with a mux: section, one without.
	writeFile(t, filepath.Join(composeDir, "alpha.yaml"), muxYAML("alpha"))
	writeFile(t, filepath.Join(composeDir, "zeta.yaml"), muxYAML("zeta"))
	writeFile(t, filepath.Join(composeDir, "plain.yaml"),
		"name: plain\ncommands:\n  a:\n    args: [echo, a]\n")

	got, err := compose.ListMuxProjects()
	assert.NilError(t, err)

	if len(got) != 2 {
		t.Fatalf("want 2 mux projects, got %d: %+v", len(got), got)
	}
	// ListNamedProjects sorts names, so the order is alpha, zeta.
	if got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Fatalf("unexpected names/order: %s, %s", got[0].Name, got[1].Name)
	}
	for _, p := range got {
		if p.Spec.Mux == nil {
			t.Fatalf("project %q should carry its loaded mux spec", p.Name)
		}
	}
}

func TestListMuxProjects_NoneWhenNoMux(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	composeDir := filepath.Join(conf, "compose")
	writeFile(t, filepath.Join(composeDir, "plain.yaml"),
		"name: plain\ncommands:\n  a:\n    args: [echo, a]\n")

	got, err := compose.ListMuxProjects()
	assert.NilError(t, err)
	if len(got) != 0 {
		t.Fatalf("want no mux projects, got %+v", got)
	}
}

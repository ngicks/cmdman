package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const muxComposeYAML = `name: tools
commands:
  web:
    args: [echo, web]
  db:
    args: [echo, db]
mux:
  driver:
    name: tmux
  layouts:
    - name: dev
      root: web
    - name: ops
      root:
        dir: h
        panes: [web, db]
`

func TestListLayoutsProjectsNamesInOrder(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "cmd-compose.yaml"), []byte(muxComposeYAML), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	b := &serviceBackend{}
	info, err := b.ListLayouts(context.Background(), "tools", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Project != "tools" {
		t.Fatalf("project = %q, want tools", info.Project)
	}
	if info.Path == "" {
		t.Fatal("path should be the discovered compose file")
	}
	want := []string{"dev", "ops"}
	if len(info.Names) != len(want) {
		t.Fatalf("layout names = %v, want %v", info.Names, want)
	}
	for i, n := range want {
		if info.Names[i] != n {
			t.Fatalf("layout names should be in definition order: got %v, want %v",
				info.Names, want)
		}
	}
	// No running dashboard for this synthetic project, so the marker is unknown.
	if info.Current != -1 {
		t.Fatalf("current marker = %d, want -1 (no running dashboard)", info.Current)
	}
}

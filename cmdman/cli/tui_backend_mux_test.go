package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outsideMux clears the two variables the enclosing-window probe is gated on
// (D13), so a test run from inside a real tmux does not let the probe reach the
// developer's own windows.
func outsideMux(t *testing.T) {
	t.Helper()
	t.Setenv("TMUX", "")
	t.Setenv("ZELLIJ", "")
}

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
	outsideMux(t)
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

// muxlessComposeYAML is the same project without a mux: section — a project
// there is no dashboard to tear down for.
const muxlessComposeYAML = `name: tools
commands:
  web:
    args: [echo, web]
`

// TestMuxDownWithoutMuxSection pins what a teardown of a project that declares
// no dashboard answers with: the resolver's complaint, which the widget shows in
// its status line, rather than a silent success that tore nothing down.
func TestMuxDownWithoutMuxSection(t *testing.T) {
	outsideMux(t)
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd-compose.yaml")
	if err := os.WriteFile(path, []byte(muxlessComposeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	// Resolution fails before the compose service is reached, so a backend
	// carrying none is all this asks for.
	b := &serviceBackend{}
	err := b.MuxDown(context.Background(), "tools", path, "")
	if err == nil {
		t.Fatal("tearing down the dashboard of a project with no mux: section should fail")
	}
	if !strings.Contains(err.Error(), "has no mux section") {
		t.Fatalf("error = %v, want it to name the missing mux section", err)
	}
}

// TestActiveIdentityOutsideMuxWithoutToken pins D13's guard: with nothing to ask
// about, nothing is asked, and the caller is handed back to cwd matching rather
// than to CurrentWindowID's answer — which outside a multiplexer is some other
// client's window, reported as if it were the caller's.
func TestActiveIdentityOutsideMuxWithoutToken(t *testing.T) {
	outsideMux(t)
	b := &serviceBackend{}
	identity, ok := b.ActiveIdentity(context.Background())
	if ok || identity != "" {
		t.Fatalf("ActiveIdentity() = %q, %v; want \"\", false outside a multiplexer", identity, ok)
	}

	_, tried := b.probeActiveIdentity(context.Background())
	if len(tried) != 1 || !strings.Contains(tried[0].String(), "not inside a multiplexer") {
		t.Fatalf("probe trail = %v, want the enclosing-window probe alone", tried)
	}
}

// TestResolveLayoutSelectionNamesEveryFailedProbe is D4 as amended by D10: when
// no project can be found the message must say which questions were asked, not
// merely that the answer was no.
func TestResolveLayoutSelectionNamesEveryFailedProbe(t *testing.T) {
	outsideMux(t)
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	// A directory with no compose file at all, so every probe has to fail.
	dir := t.TempDir()
	t.Chdir(dir)

	b := &serviceBackend{cwd: dir, workDir: dir, muxToken: "@404"}
	_, err := b.resolveLayoutSelection(context.Background(), "ghost", "")
	if err == nil {
		t.Fatal("resolving a layout selection with nothing to find should fail")
	}
	for _, want := range []string{"@404", "enclosing window", dir, "ghost"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("failure message does not name %q: %v", want, err)
		}
	}
}

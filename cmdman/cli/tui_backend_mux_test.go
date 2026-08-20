package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// TestComposeMuxUpArgv pins the command line the layout worker is run with
// against the way the project pair is read back: whatever the widget names its
// project by, the worker must resolve the same project, or it cycles somebody
// else's dashboard.
func TestComposeMuxUpArgv(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		composeFile string
		workDir     string
		want        []string
	}{
		{
			name:        "file and name travel together",
			projectName: "tools",
			composeFile: "/srv/app/cmd-compose.yaml",
			workDir:     "/srv/app",
			want: []string{
				"compose", "-f", "/srv/app/cmd-compose.yaml", "-p", "tools",
				"-w", "/srv/app", "mux", "up",
			},
		},
		{
			// With no file, the name is what -f resolves and the file's own
			// name: stands — naming it again would resolve one file into two
			// differently named projects.
			name:        "a bare name is the file key",
			projectName: "tools",
			want:        []string{"compose", "-f", "tools", "mux", "up"},
		},
		{
			name:        "a file alone leaves the declared name alone",
			composeFile: "cmd-compose.yaml",
			want:        []string{"compose", "-f", "cmd-compose.yaml", "mux", "up"},
		},
		{
			name:    "nothing named is the working directory's own project",
			workDir: "/srv/app",
			want:    []string{"compose", "-w", "/srv/app", "mux", "up"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := composeMuxUpArgv(tt.projectName, tt.composeFile, tt.workDir)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("composeMuxUpArgv() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMuxWorkerError pins that a failed worker is reported by what it said, not
// by the status it exited with: the widget has one status line and "exit status
// 1" fills it with nothing.
func TestMuxWorkerError(t *testing.T) {
	other := errors.New("resolve project: no such file")
	if got := muxWorkerError(other, "ignored"); !errors.Is(got, other) {
		t.Fatalf("an error that is not a worker's exit = %v, want it untouched", got)
	}
	if got := muxWorkerError(nil, ""); got != nil {
		t.Fatalf("muxWorkerError(nil, \"\") = %v, want nil", got)
	}

	exit := &ExitCodeError{Code: 1}
	got := muxWorkerError(exit, "cycling…\nerror: mux: spec has no layouts\n")
	if got == nil || got.Error() != "mux: spec has no layouts" {
		t.Fatalf("worker failure = %v, want the line it failed on", got)
	}
	if got := muxWorkerError(exit, "   \n\n"); !errors.Is(got, exit) {
		t.Fatalf("a worker that said nothing = %v, want its exit status", got)
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

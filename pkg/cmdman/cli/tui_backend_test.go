package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/cmdman/pkg/cmdman/tui"
)

func TestCommandInfosIncludesStandalone(t *testing.T) {
	entries := []store.CommandEntry{
		{
			ID:    "c1",
			Name:  "generated-web",
			State: model.EventTypeRunning,
			ConfigJSON: &model.CommandConfig{
				Labels: map[string]string{
					compose.LabelProject: "api-stack",
					compose.LabelWorkdir: "/work/api",
					compose.LabelCommand: "web",
				},
				LogDriver: logdriver.DriverK8sFile,
			},
		},
		{
			ID:    "c2",
			Name:  "standalone-tool",
			State: model.EventTypeExited,
			// No compose labels -> standalone; keeps its own working directory.
			ConfigJSON: &model.CommandConfig{Dir: "/work/tool"},
		},
	}
	got := commandInfos(entries)
	if len(got) != 2 {
		t.Fatalf("expected compose + standalone commands, got %d", len(got))
	}

	byID := map[string]tui.CommandInfo{}
	for _, c := range got {
		byID[c.ID] = c
	}

	web := byID["c1"]
	if web.Project != "api-stack" || web.Name != "web" {
		t.Fatalf("unexpected compose command info: %+v", web)
	}
	if web.LogDriver != logdriver.DriverK8sFile {
		t.Fatalf("log driver should propagate, got %q", web.LogDriver)
	}

	tool := byID["c2"]
	if tool.Project != "" {
		t.Fatalf("standalone command should have empty project, got %q", tool.Project)
	}
	if tool.Name != "standalone-tool" {
		t.Fatalf("standalone command name = %q, want standalone-tool", tool.Name)
	}
	if tool.Workdir != normalizePath("/work/tool") {
		t.Fatalf("standalone workdir = %q, want %q", tool.Workdir, normalizePath("/work/tool"))
	}
}

func TestMergeProjectInfosAddsZeroCommandNamedProjects(t *testing.T) {
	summaries := []compose.ProjectSummary{
		{Project: "api-stack", Commands: 3, Running: 1, WorkDir: "/work/api"},
	}
	named := []string{"api-stack", "tools"} // api-stack already known; tools is new
	got := mergeProjectInfos(summaries, named)
	if len(got) != 2 {
		t.Fatalf("expected 2 merged projects, got %d", len(got))
	}
	byName := map[string]int{}
	for _, p := range got {
		byName[p.Name] = p.Commands
	}
	if byName["api-stack"] != 3 {
		t.Fatalf("api-stack should keep its store count, got %d", byName["api-stack"])
	}
	count, ok := byName["tools"]
	if !ok {
		t.Fatalf("never-run named project tools should appear")
	}
	if count != 0 {
		t.Fatalf("never-run project should have zero commands, got %d", count)
	}
}

const cwdComposeYAML = "name: cwdproj\ncommands:\n  a:\n    args: [echo, a]\n"

func TestAppendCwdProjectAddsUnregisteredProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "cmd-compose.yaml"), []byte(cwdComposeYAML), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	got := appendCwdProject(nil)
	if len(got) != 1 {
		t.Fatalf("want 1 cwd project, got %d", len(got))
	}
	if got[0].Name != "cwdproj" {
		t.Fatalf("name = %q, want cwdproj", got[0].Name)
	}
	if got[0].Workdir != normalizePath(dir) {
		t.Fatalf("workdir = %q, want %q", got[0].Workdir, normalizePath(dir))
	}
	if got[0].Path == "" {
		t.Fatal("path should be the discovered compose file")
	}
}

func TestAppendCwdProjectFillsPathWhenAlreadyListed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "cmd-compose.yaml"), []byte(cwdComposeYAML), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	// Already listed (e.g. from the store) but with no compose-file path.
	got := appendCwdProject([]tui.ProjectInfo{{Name: "cwdproj"}})
	if len(got) != 1 {
		t.Fatalf("must not duplicate an already-listed project, got %d", len(got))
	}
	if got[0].Path == "" {
		t.Fatal("discovered path should be filled into the existing row")
	}
}

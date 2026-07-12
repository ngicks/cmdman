package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/tui"
)

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

	got := appendCwdProject(nil, "")
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

func TestProjectDefinitionReadsRawFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	content := "name: tools\ncommands:\n  a:\n    args: [echo, a]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &serviceBackend{}
	got, err := b.ProjectDefinition(context.Background(), "tools", path)
	if err != nil {
		t.Fatal(err)
	}
	if got != content {
		t.Fatalf("ProjectDefinition should return the raw file text, got %q", got)
	}
}

func TestComposeFilePathReturnsExplicitPath(t *testing.T) {
	b := &serviceBackend{}
	got, err := b.ComposeFilePath(context.Background(), "tools", "/etc/compose/tools.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/etc/compose/tools.yaml" {
		t.Fatalf("an explicit composeFile should pass through, got %q", got)
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
	got := appendCwdProject([]tui.ProjectInfo{{Name: "cwdproj"}}, "")
	if len(got) != 1 {
		t.Fatalf("must not duplicate an already-listed project, got %d", len(got))
	}
	if got[0].Path == "" {
		t.Fatal("discovered path should be filled into the existing row")
	}
}

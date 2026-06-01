package cli

import (
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

func TestComposeCommandInfosFiltersStandalone(t *testing.T) {
	entries := []store.CommandEntry{
		{
			ID:    "c1",
			Name:  "generated-web",
			State: model.EventTypeStarted,
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
			// No compose labels -> standalone, must be dropped.
			ConfigJSON: &model.CommandConfig{Labels: map[string]string{}},
		},
		{
			ID:    "c3",
			Name:  "half-labeled",
			State: model.EventTypeExited,
			// Only project label, missing workdir -> still dropped.
			ConfigJSON: &model.CommandConfig{Labels: map[string]string{
				compose.LabelProject: "x",
			}},
		},
	}
	got := composeCommandInfos(entries)
	if len(got) != 1 {
		t.Fatalf("expected only the compose-labeled command, got %d", len(got))
	}
	c := got[0]
	if c.ID != "c1" || c.Project != "api-stack" || c.Name != "web" {
		t.Fatalf("unexpected command info: %+v", c)
	}
	if c.LogDriver != logdriver.DriverK8sFile {
		t.Fatalf("log driver should propagate, got %q", c.LogDriver)
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

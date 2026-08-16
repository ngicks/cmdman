package cli

import (
	"testing"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"github.com/ngicks/cmdman/cmdman/tui"
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
				Tty:       true,
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
	if !web.Tty {
		t.Fatalf("tty should propagate from the command config")
	}

	tool := byID["c2"]
	if tool.Project != "" {
		t.Fatalf("standalone command should have empty project, got %q", tool.Project)
	}
	if tool.Tty {
		t.Fatalf("a command without tty should project Tty=false, got true")
	}
	if tool.Name != "standalone-tool" {
		t.Fatalf("standalone command name = %q, want standalone-tool", tool.Name)
	}
	if tool.Workdir != normalizePath("/work/tool") {
		t.Fatalf("standalone workdir = %q, want %q", tool.Workdir, normalizePath("/work/tool"))
	}
}

func TestCommandInfosScale(t *testing.T) {
	composeEntry := func(id string, labels map[string]string) store.CommandEntry {
		labels[compose.LabelProject] = "api-stack"
		labels[compose.LabelWorkdir] = "/work/api"
		return store.CommandEntry{
			ID:         id,
			Name:       id,
			State:      model.EventTypeRunning,
			ConfigJSON: &model.CommandConfig{Labels: labels},
		}
	}
	entries := []store.CommandEntry{
		composeEntry("r2", map[string]string{
			compose.LabelCommand:    "api",
			compose.LabelScaleIndex: "2",
			compose.LabelScale:      "3",
		}),
		// Every compose-created command is labelled, an unscaled one as 1 of 1.
		composeEntry("single", map[string]string{
			compose.LabelCommand:    "web",
			compose.LabelScaleIndex: "1",
			compose.LabelScale:      "1",
		}),
		{
			ID:         "tool",
			Name:       "standalone-tool",
			State:      model.EventTypeExited,
			ConfigJSON: &model.CommandConfig{Dir: "/work/tool"},
		},
	}

	byID := map[string]tui.CommandInfo{}
	for _, c := range commandInfos(entries) {
		byID[c.ID] = c
	}

	for _, tc := range []struct {
		id         string
		wantIndex  int
		wantCount  int
		wantReason string
	}{
		{"r2", 2, 3, "replica 2 of a command scaled to 3"},
		{"single", 0, 0, "a single-replica compose command is unscaled"},
		{"tool", 0, 0, "a standalone command carries no scale labels"},
	} {
		got := byID[tc.id]
		if got.ScaleIndex != tc.wantIndex || got.ScaleCount != tc.wantCount {
			t.Errorf(
				"%s: scale = %d/%d, want %d/%d (%s)",
				tc.id, got.ScaleIndex, got.ScaleCount, tc.wantIndex, tc.wantCount, tc.wantReason,
			)
		}
	}
}

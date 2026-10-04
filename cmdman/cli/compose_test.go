package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"gotest.tools/v3/assert"
)

// TestRenderComposePsRuntimeColumns covers the runtime half of the compose ps
// table: the same columns ls grew, filled from the same dial, with ARGV still
// trailing.
func TestRenderComposePsRuntimeColumns(t *testing.T) {
	statuses := []compose.CommandStatus{
		{
			Command:    "api",
			ID:         "id-api",
			Name:       "proj-api-1",
			State:      model.EventTypeRunning,
			Argv:       []string{"/bin/api"},
			Title:      "api-server",
			Status:     cmdman.ReportedStatusWorking,
			Detail:     "building",
			BellUnread: true,
		},
		{
			Command: "worker",
			ID:      "id-worker",
			Name:    "proj-worker-1",
			State:   model.EventTypeExited,
			Argv:    []string{"/bin/worker"},
		},
	}

	var out bytes.Buffer
	assert.NilError(t, RenderComposePs(&out, statuses, ""))

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, len(lines), 3, "output = %q", out.String())
	assert.DeepEqual(t, strings.Fields(lines[0]), []string{
		"COMMAND", "ID", "NAME", "KIND", "OWNER", "STATE", "EXIT", "CODE",
		"STATUS", "BELL", "DETAIL", "TITLE", "ARGV",
	})
	assert.DeepEqual(t, strings.Fields(lines[1]), []string{
		"api", "id-api", "proj-api-1", "-", "-", "running", "-",
		"working", "*", "building", "api-server", "/bin/api",
	})
	assert.DeepEqual(t, strings.Fields(lines[2]), []string{
		"worker", "id-worker", "proj-worker-1", "-", "-", "exited",
		"-", "-", "-", "-", "-", "/bin/worker",
	})

	out.Reset()
	assert.NilError(t, RenderComposePs(&out, statuses, "{{.Command}}={{.Status}}/{{.Title}}"))
	assert.Equal(t, out.String(), "api=working/api-server\nworker=/\n")
}

func TestRenderComposePsIntermediateRows(t *testing.T) {
	statuses := []compose.CommandStatus{
		{
			Command: "web",
			ID:      "id-web",
			Name:    "proj-web-1",
			State:   model.EventTypeRunning,
			Argv:    []string{"sleep", "300"},
		},
		{
			Command:      "web",
			ID:           "id-holder",
			Name:         "proj-web-1.res.scratch",
			Intermediate: compose.IntermediateHolder,
			Owner:        "proj-web-1",
			State:        model.EventTypeCreated,
			Argv:         []string{"true"},
		},
		{
			ID:           "id-exec",
			Name:         "proj-old-1.hook.cleanup.remove_post",
			Intermediate: compose.IntermediateExec,
			Owner:        "proj-old-1",
			State:        model.EventTypeExited,
			ExitCode:     new(1),
			Argv:         []string{"false"},
		},
	}

	var out bytes.Buffer
	assert.NilError(t, RenderComposePs(&out, statuses, ""))
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, len(lines), 4, "output = %q", out.String())
	assert.DeepEqual(t, strings.Fields(lines[1])[:5], []string{
		"web", "id-web", "proj-web-1", "-", "-",
	})
	assert.DeepEqual(t, strings.Fields(lines[2])[:6], []string{
		"web", "id-holder", "proj-web-1.res.scratch", "holder", "proj-web-1", "created",
	})
	assert.DeepEqual(t, strings.Fields(lines[3])[:7], []string{
		"-", "id-exec", "proj-old-1.hook.cleanup.remove_post", "exec", "proj-old-1",
		"exited", "1",
	})

	out.Reset()
	assert.NilError(t, RenderComposePs(&out, statuses,
		"{{.Name}} {{or .Intermediate \"replica\"}} {{.Owner}}"))
	assert.Equal(t, out.String(), "proj-web-1 replica \n"+
		"proj-web-1.res.scratch holder proj-web-1\n"+
		"proj-old-1.hook.cleanup.remove_post exec proj-old-1\n")

	out.Reset()
	assert.NilError(t, RenderComposePs(&out, statuses, "json"))
	var decoded []map[string]any
	assert.NilError(t, json.Unmarshal(out.Bytes(), &decoded))
	_, replicaHasKind := decoded[0]["Intermediate"]
	assert.Assert(t, !replicaHasKind, "a replica omits the Intermediate field: %v", decoded[0])
	assert.Equal(t, decoded[1]["Intermediate"], compose.IntermediateHolder)
	assert.Equal(t, decoded[1]["Owner"], "proj-web-1")
}

func TestRenderComposeProjectsIntermediatesColumn(t *testing.T) {
	summaries := []compose.ProjectSummary{
		{
			Project:       "proj",
			WorkDir:       "/wd",
			ComposeFile:   "/wd/cmd-compose.yaml",
			Commands:      2,
			Running:       2,
			Intermediates: 3,
		},
		{Project: "gone", WorkDir: "/old", Intermediates: 1},
	}

	var out bytes.Buffer
	assert.NilError(t, RenderComposeProjects(&out, summaries, ""))
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, len(lines), 3, "output = %q", out.String())
	assert.DeepEqual(t, strings.Fields(lines[0]), []string{
		"PROJECT", "COMMANDS", "RUNNING", "EXITED", "FAILED", "INTERMEDIATES", "WORKDIR", "FILE",
	})
	assert.DeepEqual(t, strings.Fields(lines[1]), []string{
		"proj", "2", "2", "0", "0", "3", "/wd", "/wd/cmd-compose.yaml",
	})
	assert.DeepEqual(t, strings.Fields(lines[2]), []string{
		"gone", "0", "0", "0", "0", "1", "/old",
	})

	out.Reset()
	assert.NilError(t, RenderComposeProjects(&out, summaries, "{{.Project}}={{.Intermediates}}"))
	assert.Equal(t, out.String(), "proj=3\ngone=1\n")
}

func TestPrintComposeLogsPrefixesTimeAndCommand(t *testing.T) {
	ts := time.Date(2026, 5, 24, 1, 2, 3, 456789000, time.UTC)
	msgs := make(chan compose.LogMessage, 1)
	msgs <- compose.LogMessage{
		Command: "alpha",
		Record: logdriver.Record{
			Line: logdriver.LogLine{
				Time:   ts,
				Stream: logdriver.StreamStdout,
				Line:   []byte("line-from-alpha\n"),
			},
		},
	}
	close(msgs)

	var stdout, stderr bytes.Buffer
	err := PrintComposeLogs(&stdout, &stderr, msgs)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "2026-05-24T01:02:03.456789Z alpha |line-from-alpha\n")
	assert.Equal(t, stderr.String(), "")
}

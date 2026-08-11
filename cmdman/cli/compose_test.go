package cli

import (
	"bytes"
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
		"COMMAND", "ID", "NAME", "STATE", "EXIT", "CODE",
		"STATUS", "BELL", "DETAIL", "TITLE", "ARGV",
	})
	assert.DeepEqual(t, strings.Fields(lines[1]), []string{
		"api", "id-api", "proj-api-1", "running", "-",
		"working", "*", "building", "api-server", "/bin/api",
	})
	assert.DeepEqual(t, strings.Fields(lines[2]), []string{
		"worker", "id-worker", "proj-worker-1", "exited", "-", "-", "-", "-", "-", "/bin/worker",
	})

	out.Reset()
	assert.NilError(t, RenderComposePs(&out, statuses, "{{.Command}}={{.Status}}/{{.Title}}"))
	assert.Equal(t, out.String(), "api=working/api-server\nworker=/\n")
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

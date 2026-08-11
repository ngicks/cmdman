package cli

import (
	"bytes"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/model"
)

func TestResolveStatusTarget(t *testing.T) {
	got, err := ResolveStatusTarget([]string{"by-arg"}, "by-env")
	assert.NilError(t, err)
	assert.Equal(t, got, "by-arg")

	got, err = ResolveStatusTarget(nil, "by-env")
	assert.NilError(t, err)
	assert.Equal(t, got, "by-env")

	_, err = ResolveStatusTarget(nil, "")
	assert.ErrorContains(t, err, cmdman.ENV_CMDMAN_CMD_ID)
}

func TestRenderReportedStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		info   cmdman.ReportedStatusInfo
		format string
		want   string
	}{
		{
			name: "nothing reported prints nothing",
			info: cmdman.ReportedStatusInfo{},
		},
		{
			name: "status alone",
			info: cmdman.ReportedStatusInfo{Status: cmdman.ReportedStatusDone},
			want: "done\n",
		},
		{
			name: "detail follows a tab",
			info: cmdman.ReportedStatusInfo{
				Status: cmdman.ReportedStatusWaiting,
				Detail: "needs input",
			},
			want: "waiting\tneeds input\n",
		},
		{
			name:   "template",
			info:   cmdman.ReportedStatusInfo{Status: cmdman.ReportedStatusWorking},
			format: "{{.Status}}!",
			want:   "working!\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			assert.NilError(t, RenderReportedStatus(&out, tc.info, tc.format))
			assert.Equal(t, out.String(), tc.want)
		})
	}

	t.Run("json omits what was not reported", func(t *testing.T) {
		var out bytes.Buffer
		assert.NilError(t, RenderReportedStatus(
			&out,
			cmdman.ReportedStatusInfo{Status: cmdman.ReportedStatusDone},
			"json",
		))
		assert.Equal(t, out.String(), "{\n  \"Status\": \"done\"\n}\n")
	})
}

func TestRenderComposeStatus(t *testing.T) {
	states := []compose.CommandRuntimeState{
		{
			Command:    "api",
			ID:         "id-api",
			Name:       "proj-api-1",
			State:      model.EventTypeRunning,
			Title:      "api server",
			Status:     cmdman.ReportedStatusWaiting,
			Detail:     "needs-input",
			BellUnread: true,
		},
		{
			Command: "worker",
			ID:      "id-worker",
			Name:    "proj-worker-1",
			State:   model.EventTypeExited,
		},
	}

	var out bytes.Buffer
	assert.NilError(t, RenderComposeStatus(&out, states, ""))

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, len(lines), 3, "output = %q", out.String())
	assert.DeepEqual(t, strings.Fields(lines[0]),
		[]string{"COMMAND", "STATE", "STATUS", "BELL", "DETAIL", "TITLE"})
	// Cells here carry no internal spaces except TITLE, the trailing column.
	assert.DeepEqual(t, strings.Fields(lines[1]),
		[]string{"api", "running", "waiting", "*", "needs-input", "api", "server"})
	// Nothing reported: the placeholders keep the columns readable.
	assert.DeepEqual(t, strings.Fields(lines[2]),
		[]string{"worker", "exited", "-", "-", "-"})

	out.Reset()
	assert.NilError(t, RenderComposeStatus(&out, states, "{{.Command}}={{.Status}}"))
	assert.Equal(t, out.String(), "api=waiting\nworker=\n")

	out.Reset()
	assert.NilError(t, RenderComposeStatus(&out, nil, "json"))
	assert.Equal(t, out.String(), "[]\n")
}

package cli

import (
	"bytes"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

func TestRenderEntriesExitCode(t *testing.T) {
	tests := []struct {
		name     string
		exitCode *int
		want     string // expected value in the EXIT CODE column
	}{
		{name: "nil", exitCode: nil, want: "-"},
		{name: "zero", exitCode: new(int), want: "0"},
		{name: "nonzero", exitCode: new(42), want: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := RenderEntries(&out, []store.CommandEntry{{
				ID:       "123456789abc",
				Name:     "test",
				State:    model.EventTypeExited,
				ExitCode: tt.exitCode,
				ConfigJSON: &model.CommandConfig{
					Argv: []string{"/bin/true"},
				},
			}}, nil, false, "")
			assert.NilError(t, err)

			// Header row + one data row, columns padded with spaces (out is a
			// bytes.Buffer, so terminalWidth == 0 → no truncation).
			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
			assert.Equal(t, len(lines), 2, "output = %q", out.String())

			// Cells here contain no internal spaces, so Fields recovers the
			// columns: ID NAME STATE EXIT-CODE STATUS DETAIL BELL TITLE
			// COMMAND. Nothing was dialed, so every runtime cell is "-".
			fields := strings.Fields(lines[1])
			want := []string{
				"123456789abc", "test", "exited", tt.want,
				"-", "-", "-", "-", "/bin/true",
			}
			assert.DeepEqual(t, fields, want)
		})
	}
}

// TestRenderEntriesRuntimeColumns covers the runtime half of the ls table: a
// command whose monitor answered shows what it said about itself, and one with
// no answer keeps the placeholders the rest of the table uses — the case every
// command with a dead or missing monitor lands in.
func TestRenderEntriesRuntimeColumns(t *testing.T) {
	entries := []store.CommandEntry{
		{
			ID:         "id-api",
			Name:       "api",
			State:      model.EventTypeRunning,
			ConfigJSON: &model.CommandConfig{Argv: []string{"/bin/api"}},
		},
		{
			ID:         "id-worker",
			Name:       "worker",
			State:      model.EventTypeExited,
			ExitCode:   new(0),
			ConfigJSON: &model.CommandConfig{Argv: []string{"/bin/worker"}},
		},
	}
	runtime := map[string]cmdman.RuntimeState{
		"id-api": {
			Title:      "api-server",
			Status:     cmdman.ReportedStatusWaiting,
			Detail:     "needs-input",
			BellUnread: true,
		},
	}

	var out bytes.Buffer
	assert.NilError(t, RenderEntries(&out, entries, runtime, false, ""))

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	assert.Equal(t, len(lines), 3, "output = %q", out.String())
	assert.DeepEqual(t, strings.Fields(lines[0]), []string{
		"ID", "NAME", "STATE", "EXIT", "CODE", "STATUS", "BELL", "DETAIL", "TITLE", "COMMAND",
	})
	assert.DeepEqual(t, strings.Fields(lines[1]), []string{
		"id-api", "api", "running", "-", "waiting", "*", "needs-input", "api-server", "/bin/api",
	})
	assert.DeepEqual(t, strings.Fields(lines[2]), []string{
		"id-worker", "worker", "exited", "0", "-", "-", "-", "-", "/bin/worker",
	})

	// The same values under a user --format, where they are the raw fields
	// rather than the table's placeholders.
	out.Reset()
	assert.NilError(t, RenderEntries(&out, entries, runtime, false,
		`{{.Name}}={{.Status}}/{{.Detail}}/{{bell .BellUnread}}/{{.Title}}`))
	assert.Equal(t, out.String(), "api=waiting/needs-input/*/api-server\nworker=//-/\n")

	// Quiet mode is IDs only: it never asks for runtime state, so it must not
	// print any either.
	out.Reset()
	assert.NilError(t, RenderEntries(&out, entries, runtime, true, ""))
	assert.Equal(t, out.String(), "id-api\nid-worker\n")
}

// TestFormatUsageListsRuntimeFields pins that --format's help names the runtime
// fields; they are only discoverable through it.
func TestFormatUsageListsRuntimeFields(t *testing.T) {
	usage := FormatUsage()
	for _, want := range []string{".ID", ".Title", ".Status", ".Detail", ".BellUnread", "bell"} {
		assert.Assert(t, strings.Contains(usage, want), "usage lacks %q:\n%s", want, usage)
	}
}

// TestTitleText covers the TITLE cell: no title reads as the table's "nothing
// here", and a long one is cut to the column instead of pushing COMMAND off the
// line.
func TestTitleText(t *testing.T) {
	assert.Equal(t, titleText(""), "-")
	assert.Equal(t, titleText("short"), "short")

	got := titleText(strings.Repeat("x", titleMaxLen+10))
	assert.Equal(t, width(got), titleMaxLen)
	assert.Assert(t, strings.HasSuffix(got, "…"), "got %q", got)
}

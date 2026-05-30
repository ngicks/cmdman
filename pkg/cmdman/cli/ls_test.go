package cli

import (
	"bytes"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
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
			}}, false, "")
			assert.NilError(t, err)

			// Header row + one data row, columns padded with spaces (out is a
			// bytes.Buffer, so terminalWidth == 0 → no truncation).
			lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
			assert.Equal(t, len(lines), 2, "output = %q", out.String())

			// Cells here contain no internal spaces, so Fields recovers the
			// columns: ID NAME STATE EXIT-CODE COMMAND.
			fields := strings.Fields(lines[1])
			want := []string{"123456789abc", "test", "exited", tt.want, "/bin/true"}
			assert.DeepEqual(t, fields, want)
		})
	}
}

func TestTemplateDeref(t *testing.T) {
	v := new(42)
	assert.Equal(t, deref(v), 42)
	assert.Equal(t, deref(nil), nil)
}

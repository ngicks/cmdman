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
		want     string
	}{
		{
			name:     "nil",
			exitCode: nil,
			want:     "\texited\t-\t",
		},
		{
			name:     "zero",
			exitCode: new(int),
			want:     "\texited\t0\t",
		},
		{
			name:     "nonzero",
			exitCode: new(42),
			want:     "\texited\t42\t",
		},
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
			assert.Assert(
				t,
				strings.Contains(out.String(), tt.want),
				"output = %q, want substring %q",
				out.String(),
				tt.want,
			)
		})
	}
}

func TestTemplateDeref(t *testing.T) {
	v := new(42)
	assert.Equal(t, deref(v), 42)
	assert.Equal(t, deref(nil), nil)
}

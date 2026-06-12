package compose_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func TestMuxValidate_UnknownCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd-compose.yaml")
	content := `
name: mux-validate-test
commands:
  api:
    args: [echo, api]
mux:
  layouts:
    - name: main
      root:
        command: nonexistent
`
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))

	raw, err := compose.DecodeFile(path)
	assert.NilError(t, err)
	_, err = compose.Normalize(context.Background(), path, raw, compose.NormalizeOpts{})
	assert.Assert(t, err != nil)
	assert.Assert(t, cmp.Contains(err.Error(), "unknown command"))
	assert.Assert(t, cmp.Contains(err.Error(), "nonexistent"))
}

func TestMuxValidate_PinnedScaleExceedsCommandScale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd-compose.yaml")
	content := `
name: mux-validate-test
commands:
  web:
    args: [echo, web]
    scale: 2
mux:
  layouts:
    - name: main
      root:
        command: web
        scale: 3
`
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))

	raw, err := compose.DecodeFile(path)
	assert.NilError(t, err)
	_, err = compose.Normalize(context.Background(), path, raw, compose.NormalizeOpts{})
	assert.Assert(t, err != nil)
	assert.Assert(t, cmp.Contains(err.Error(), "scale 3 exceeds"))
	assert.Assert(t, cmp.Contains(err.Error(), "commands.web.scale 2"))
}

func TestMuxValidate_PinnedScaleEqualsCommandScale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd-compose.yaml")
	content := `
name: mux-validate-test
commands:
  web:
    args: [echo, web]
    scale: 2
mux:
  layouts:
    - name: main
      root:
        command: web
        scale: 2
`
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))

	raw, err := compose.DecodeFile(path)
	assert.NilError(t, err)
	_, err = compose.Normalize(context.Background(), path, raw, compose.NormalizeOpts{})
	assert.NilError(t, err)
}

func TestMuxValidate_AbsentScaleNeverErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd-compose.yaml")
	content := `
name: mux-validate-test
commands:
  web:
    args: [echo, web]
    scale: 2
mux:
  layouts:
    - name: main
      root:
        command: web
`
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))

	raw, err := compose.DecodeFile(path)
	assert.NilError(t, err)
	_, err = compose.Normalize(context.Background(), path, raw, compose.NormalizeOpts{})
	assert.NilError(t, err)
}
